module github.com/ipfs/go-ds-s3

require (
	github.com/aws/aws-sdk-go v1.25.7
	github.com/ipfs/go-datastore v0.3.1
	github.com/ipfs/go-ipfs v0.4.22
)

go 1.11

replace github.com/go-critic/go-critic v0.0.0-20181204210945-ee9bf5809ead => github.com/go-critic/go-critic v0.4.0

replace github.com/golangci/golangci-lint v1.16.1-0.20190425135923-692dacb773b7 => github.com/golangci/golangci-lint v1.21.0
