build:
	CGO_ENABLED=0 go build -o ecs-rebalancer .

update_packages:
	go get -u all

test_docker_build:
	docker build -t ecs-rebalancer .
