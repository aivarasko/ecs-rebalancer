package rebalancer

import (
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/codedeploy"
	"github.com/aws/aws-sdk-go/service/ecs"
	log "github.com/sirupsen/logrus"
	"time"
)

type RebalanceConfig struct {
	Cluster                   string
	Service                   string
	DeploymentApplicationName string
	DeploymentGroup           string
}

type Rebalancer struct {
	svcECS          *ecs.ECS
	svcCodeDeploy   *codedeploy.CodeDeploy
	config          RebalanceConfig
	currentCapacity int64
	currentTasks    map[string]*ecs.Task
}

func (r *Rebalancer) Init(config RebalanceConfig) {
	r.svcECS = ecs.New(session.New(), aws.NewConfig())
	r.svcCodeDeploy = codedeploy.New(session.New(), aws.NewConfig())
	r.config = config
	r.currentTasks = map[string]*ecs.Task{}
}

func (r Rebalancer) listDeploymentsInProgress() bool {
	//log.Info("Waiting for service to be stable ", clusterName, *service)
	listDeploymentsInput := &codedeploy.ListDeploymentsInput{
		IncludeOnlyStatuses: []*string{
			aws.String("Created"),
			aws.String("Queued"),
			aws.String("InProgress"),
		},
		ApplicationName:     aws.String(r.config.DeploymentApplicationName),
		DeploymentGroupName: aws.String(r.config.DeploymentGroup),
	}

	listDeploymentsResult, err := r.svcCodeDeploy.ListDeployments(listDeploymentsInput)
	if err != nil {
		log.Fatal(err)
	}
	for _, deploymentInProgress := range listDeploymentsResult.Deployments {
		log.Debug("Deployment in progress ", r.config.DeploymentApplicationName, " ", r.config.DeploymentGroup, " ", *deploymentInProgress)
	}
	if len(listDeploymentsResult.Deployments) != 0 {
		return true
	}

	return false
}

func (r Rebalancer) syncTasks(tasks []*ecs.Task) {
	currentTaskArns := map[string]bool{}

	for _, task := range tasks {
		_, ok := r.currentTasks[*task.TaskArn]
		if !ok {
			log.Info(fmt.Sprintf("Registering task - %s %s %s %s", *task.TaskArn, *task.TaskDefinitionArn, *task.DesiredStatus, *task.HealthStatus))
			r.currentTasks[*task.TaskArn] = task
		}
		currentTaskArns[*task.TaskArn] = true
	}

	for _, task := range r.currentTasks {
		_, ok := currentTaskArns[*task.TaskArn]
		if !ok {
			log.Info("DeRegistering task", task)
			delete(r.currentTasks, *task.TaskArn)
		}
	}
}

func (r Rebalancer) rebalanceContainerDuplicates() {
	listContainerInstancesInput := &ecs.ListContainerInstancesInput{
		Cluster: aws.String(r.config.Cluster),
	}

	listContainerInstances, err := r.svcECS.ListContainerInstances(listContainerInstancesInput)
	if err != nil {
		log.Fatal(err)
	}

	instances := map[string]int{}
	for _, instance := range listContainerInstances.ContainerInstanceArns {
		instances[*instance] = 0
	}

	listTasksInput := &ecs.ListTasksInput{
		Cluster:       aws.String(r.config.Cluster),
		ServiceName:   aws.String(r.config.Service),
		DesiredStatus: aws.String("RUNNING"),
	}

	listTasksResult, err := r.svcECS.ListTasks(listTasksInput)
	if err != nil {
		log.Fatal(err)
	}

	describeTasksInput := &ecs.DescribeTasksInput{
		Cluster: aws.String(r.config.Cluster),
		Tasks:   listTasksResult.TaskArns,
	}

	describeTasksResult, err := r.svcECS.DescribeTasks(describeTasksInput)
	if err != nil {
		log.Fatal(err)
	}

	r.syncTasks(describeTasksResult.Tasks)

	for _, v := range describeTasksResult.Tasks {
		instances[*v.ContainerInstanceArn] += 1

		if instances[*v.ContainerInstanceArn] > 1 {
			log.Warn("Instance ", r.config.Service, " ", *v.ContainerInstanceArn, " with multiple containers")

			log.Info("Stopping ", *v.TaskArn, " ", *v.TaskArn)
			stopTasksInput := &ecs.StopTaskInput{
				Cluster: aws.String(r.config.Cluster),
				Task:    v.TaskArn,
			}

			_, err := r.svcECS.StopTask(stopTasksInput)
			if err != nil {
				log.Fatal(err)
			}
		}
	}

	for instance, value := range instances {
		if value != 1 {
			log.Info("Instance ", instance, " with ", value, " ", r.config.Service)
		}
	}

}

func (r Rebalancer) describeClustersCapacity(clusterARN string) int64 {
	input := &ecs.DescribeClustersInput{
		Clusters: []*string{
			aws.String(clusterARN),
		},
	}

	result, err := r.svcECS.DescribeClusters(input)
	if err != nil {
		log.Fatal(err)
	}

	return *result.Clusters[0].RegisteredContainerInstancesCount
}

func (r Rebalancer) updateDesiredCount(desiredCount int64) {
	log.Info(fmt.Sprintf("updateDesiredCount %s %s %d", r.config.Cluster, r.config.Service, desiredCount))

	input := &ecs.UpdateServiceInput{
		Cluster:      aws.String(r.config.Cluster),
		Service:      aws.String(r.config.Service),
		DesiredCount: &desiredCount,
	}

	_, err := r.svcECS.UpdateService(input)
	if err != nil {
		log.Fatal(err)
	}
	//log.Info(result)
}

func (r *Rebalancer) rebalanceServiceCapacity() bool {
	clusterCapacity := r.describeClustersCapacity(r.config.Cluster)
	if clusterCapacity != r.currentCapacity {
		log.Info("ClustersCapacity ", r.config.Cluster, " ", clusterCapacity)
		r.currentCapacity = clusterCapacity
	}

	input := &ecs.DescribeServicesInput{
		Cluster: aws.String(r.config.Cluster),
		Services: []*string{
			aws.String(r.config.Service),
		},
	}

	result, err := r.svcECS.DescribeServices(input)
	if err != nil {
		log.Fatal(err)
	}

	for _, service := range result.Services {
		if *service.DesiredCount != clusterCapacity {
			r.updateDesiredCount(clusterCapacity)

			return true
		}
	}

	return false
}

func (r Rebalancer) RunReconcile(loopFrequencySeconds int, coolDownSeconds int) {
	coolDownTill := time.Now().Local().Add(time.Second * time.Duration(loopFrequencySeconds))

	for {
		hasDeploymentsInProgress := r.listDeploymentsInProgress()
		if hasDeploymentsInProgress {
			coolDownTill = time.Now().Local().Add(time.Second * time.Duration(coolDownSeconds))
		}
		hasCoolDownPassed := time.Now().Local().Sub(coolDownTill) > 0

		if !hasDeploymentsInProgress && hasCoolDownPassed {
			if r.rebalanceServiceCapacity() {
				// Give time for rebalance to set in
				time.Sleep(60 * time.Second)
			}

			r.rebalanceContainerDuplicates()
		} else if !hasDeploymentsInProgress {
			log.Debug("Skipping - cooldown ", time.Now().Local().Sub(coolDownTill))
		}
		time.Sleep(time.Duration(loopFrequencySeconds) * time.Second)
	}
}
