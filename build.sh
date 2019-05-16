#!/usr/bin/env bash

go get ./...
GOOS=linux GOARCH=amd64 go build -o bin/application