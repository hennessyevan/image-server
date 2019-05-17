#!/bin/bash
GOBIN=$GOPATH/bin
go get
go build -o bin/application application.go