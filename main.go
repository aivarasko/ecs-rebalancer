package main

import (
	"flag"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sts"
	log "github.com/sirupsen/logrus"
	"main/rebalancer"
	"os"
)

func init() {
	// Log as JSON instead of the default ASCII formatter.
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
	})

	// Output to stdout instead of the default stderr
	// Can be any io.Writer, see below for File example
	log.SetOutput(os.Stdout)

	// Only log the warning severity or above.
	log.SetLevel(log.DebugLevel)
}

func getCallerIdentity() {
	svc := sts.New(session.New())
	input := &sts.GetCallerIdentityInput{}

	result, err := svc.GetCallerIdentity(input)
	if err != nil {
		log.Fatal(err)
	}

	log.Info(result)
}

func main() {
	cluster := flag.String("cluster", "", "")
	service := flag.String("service", "", "")
	deploymentApplicationName := flag.String("deploymentApplicationName", "", "")
	deploymentGroup := flag.String("deploymentApplicationGroup", "", "")
	flag.Parse()

	config := rebalancer.RebalanceConfig{
		Cluster:                   *cluster,
		Service:                   *service,
		DeploymentApplicationName: *deploymentApplicationName,
		DeploymentGroup:           *deploymentGroup}

	rebalanceInstance := rebalancer.Rebalancer{}
	rebalanceInstance.Init(config)

	getCallerIdentity()

	coolDownSeconds := 100
	loopFrequencySeconds := 5
	rebalanceInstance.RunReconcile(loopFrequencySeconds, coolDownSeconds)
}
