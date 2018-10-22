.PHONY: deps clean build

deps:
	dep ensure
#	go get -u ./...

clean: 
	rm -rf ./build/*
	
build:
	GOOS=linux GOARCH=amd64 go build -o build/cloudtrail-lambda-unomaly-auditusecase ./cloudtrailToUnomaly/main.go

package:
	sam package \
		--template-file template.yaml \
		--output-template-file packaged.yaml \
		--s3-bucket unomaly-lambda-functions-london

deploy:
	sam deploy \
		--template-file packaged.yaml \
		--stack-name cloudtrail-lambda-unomaly-auditusecase \
		--capabilities CAPABILITY_IAM