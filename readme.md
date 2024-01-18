# Integration Testing with Testcontainers 

This project demonstrates how to perform integration testing using
test containers to spin up the infrastructure the application depends
on in docker containers and then tests your application against them.


## Prerequisites 
1. Golang 1.23.1
2. Docker version 24.0.6

You can learn more about the system requirements for test containers 
here https://golang.testcontainers.org/system_requirements

## Setup
1. Clone this repository
2. Run `go get ./...`
3. Run `go test -cover -v ./...`