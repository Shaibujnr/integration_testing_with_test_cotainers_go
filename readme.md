# Integration Testing with Testcontainers :white_check_mark:

This project demonstrates how to perform integration testing using
test containers to spin up the infrastructure the application depends
on in docker containers and then tests your application against them.


* The project implements a simple notes application that allows creating, updating and retrieving notes
* The application uses a repository  to manage the storage and retrieval of the notes.
* The repository uses postgres database as primary storage and redis for cache
* The repository implements a `read-through` caching strategy. It first checks the cache (redis) for a note and
if it can't find the note in the cache, it checks the primary storage (postgres). If it finds the note, it updates 
the cache with the retrieved note before returning the note to the caller. Subsequent calls to retrieve the note
will use the cache and prevent hits to the postgres database.
* Every write operation (update, delete) will invalidate the cache to ensure consistency.


## Prerequisites 
1. Golang 1.23.1
2. Docker version 24.0.6

You can learn more about the system requirements for test containers 
here https://golang.testcontainers.org/system_requirements

## Setup
1. Clone this repository
2. Run `go mod tidy`
3. Run `go test -cover -v ./...`
