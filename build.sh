#!/usr/bin/env bash

go get -d ./...
GOOS=linux GOARCH=amd64 go build -o bin/application -ldflags="-s -w"