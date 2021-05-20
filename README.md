terraform-provider-sealedsecrets
================================

This is a fork of [rockyhmchen's terraform-provider-sealedsecrets](https://github.com/rockyhmchen/terraform-provider-sealedsecrets).

The `sealedsecrets` provider helps you manage SealedSecret objects (`bitnami.com/v1alpha1`) from terraform. It generates a
K8s Secret from the key/value pairs you give as input, encrypts it using `kubeseal` and finally applies it to the cluster.
In subsequent runs it will check if the object still exists in the cluster or if the contents have changed and act accordingly.


### Usage

```HCL
terraform {
  required_providers {
    sealedsecrets = {
      source = "kita99/sealedsecrets"
      version = "0.2.0"
    }
  }
}

provider "sealedsecrets" {
  # optional
  kubectl_bin = "/usr/local/bin/kubectl"

  # optional
  kubeseal_bin = "/usr/local/bin/kubeseal"
}

resource "sealedsecrets_secret" "my_secret" {
  name = "my-secret"
  namespace = kubernetes_namespace.example_ns.metadata.0.name
  type = "Opaque"

  secrets = {
    key = "value"
  }
  controller_name = "sealed-secret-controller"
  controller_namespace = "default"

  depends_on = [kubernetes_namespace.example_ns, var.sealed_secrets_controller_id]
}
```

### Argument Reference

The following arguments are supported:
- `name` - Name of the secret, must be unique.
- `namespace` - Namespace defines the space within which name of the secret must be unique.
- `type` -  The secret type. ex: `Opaque`
- `secrets` - Key/value pairs to populate the secret
- `controller_name` - Name of the SealedSecrets controller in the cluster
- `controller_namespace` - Namespace of the SealedSecrets controller in the cluster
- `depends_on` - For specifying hidden dependencies.

*NOTE: All the arguments above are required*


### Behind the scenes

#### Create

Takes resource inputs to form the below command, computes SHA256 hash of the resulting SealedSecret manifest and sets it as the resource id.

```bash
    kubectl create secret generic {sealedsecrets_secret.[resource].name} \    
    --namespace={sealedsecrets_secret.[resource].namespace} \    
    --type={sealedsecrets_secret.[resource].type} \    
    --from-literal={sealedsecrets_secret.[resource].secrets.key}={sealedsecrets_secret.[resource].secrets.value} \ # line repeated for each key/value pair
    --dry-run \    
    --output=yaml | \    
    kubeseal \    
    --controller-name ${sealedsecrets_secret.[resource].controller_name} \    
    --controller-namespace ${sealedsecrets_secret.[resource].controller_namespace} \    
    --format yaml \
    > /tmp/sealedsecret.yaml
```


#### Read

Checks if the SealedSecret object still exists in the cluster or if the SHA256 hash has changed.


#### Update

Same as `Create`


#### Delete

Removes SealedSecret object from the cluster and deletes terraform state.
