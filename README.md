# DIMO Credit Tracker

![GitHub license](https://img.shields.io/badge/license-Apache%202.0-blue.svg)
[![GoDoc](https://godoc.org/github.com/DIMO-Network/credit-tracker?status.svg)](https://godoc.org/github.com/DIMO-Network/credit-tracker)
[![Go Report Card](https://goreportcard.com/badge/github.com/DIMO-Network/credit-tracker)](https://goreportcard.com/report/github.com/DIMO-Network/credit-tracker)

The DIMO Credit Tracker is a service that manages and tracks credits within the DIMO Developer.

## Prerequisites

- Go 1.24 or later
- Docker (for containerized deployment)

## Configuration

The service is configured using a YAML settings file. A sample configuration file is provided in `settings.sample.yaml`. Copy this file to `settings.yaml` and adjust the settings as needed.

## Development

### Available Make Commands

```shell
> make help
Specify a subcommand:
  build                build the binary
  run                  run the binary
  clean                clean the binary
  install              install the binary
  tidy                 tidy the go mod
  test                 run tests
  lint                 run linter
  docker               build docker image
  tools-golangci-lint  install golangci-lint
  tools-protoc         install protoc
  generate             run all file generation for the project
  generate-swagger     generate swagger documentation
  generate-go          run go generate
  generate-grpc        generate grpc files
```

## API Documentation

The API documentation is available via Swagger UI `/swagger` when the service is running. The documentation is automatically generated from the code annotations.
