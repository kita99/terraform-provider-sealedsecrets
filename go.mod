module github.com/kita99/terraform-provider-sealedsecrets

go 1.16

require (
	github.com/aws/aws-sdk-go v1.30.12 // indirect
	github.com/bitnami-labs/sealed-secrets v0.16.0
	github.com/cenkalti/backoff v2.2.1+incompatible
	github.com/hashicorp/hcl/v2 v2.6.0 // indirect
	github.com/hashicorp/terraform-plugin-sdk/v2 v2.6.1
	github.com/icza/dyno v0.0.0-20200205103839-49cb13720835
	github.com/mitchellh/go-homedir v1.1.0
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/api v0.21.1
	k8s.io/apimachinery v0.21.1
	k8s.io/cli-runtime v0.21.1
	k8s.io/client-go v0.21.1
	k8s.io/kube-aggregator v0.21.1
	k8s.io/kubectl v0.21.1
	sigs.k8s.io/yaml v1.2.0
)
