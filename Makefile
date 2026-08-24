.PHONY: test race vet build run measure

test:
	go test ./... -count=1

race:
	go test -race ./... -count=1

vet:
	go vet ./...

build:
	go build ./...

run:
	go run ./cmd/server

measure:
	go run ../.agents/skills/go-base-project-create/scripts/measure_project.go -root .

