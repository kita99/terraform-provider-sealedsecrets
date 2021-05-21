package sealedsecrets

import (
	"fmt"
	"log"
    b64 "encoding/base64"
    "context"
    "time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
    "github.com/cenkalti/backoff"

	"github.com/kita99/terraform-provider-sealedsecrets/utils/kubeseal"
	"github.com/kita99/terraform-provider-sealedsecrets/utils/kubectl"
	"github.com/kita99/terraform-provider-sealedsecrets/utils"
)

func resourceSecret() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceSecretCreate,
		ReadContext:   resourceSecretRead,
		UpdateContext: resourceSecretUpdate,
		DeleteContext: resourceSecretDelete,

		Schema: map[string]*schema.Schema{
			"name": &schema.Schema{
				Type:        schema.TypeString,
				Required:    true,
				Description: "Name of the secret, must be unique",
			},
			"namespace": &schema.Schema{
				Type:        schema.TypeString,
				Required:    true,
				Description: "Namespace of the secret",
			},
			"type": &schema.Schema{
				Type:        schema.TypeString,
				Required:    true,
				Description: "The secret type (ex. Opaque)",
			},
			"secrets": &schema.Schema{
				Type:        schema.TypeMap,
				Required:    true,
				Description: "Key/value pairs to populate the secret",
			},
			"controller_name": &schema.Schema{
				Type:        schema.TypeString,
				Required:    true,
				Description: "Name of the SealedSecrets controller in the cluster",
			},
			"controller_namespace": &schema.Schema{
				Type:        schema.TypeString,
				Required:    true,
				Description: "Namespace of the SealedSecrets controller in the cluster",
			},
			"manifest": &schema.Schema{
				Type:        schema.TypeString,
				Computed:    true,
				Description: "",
			},
		},
	}
}

func resourceSecretCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	log.Printf("resourceSecretCreate")
    // 1. Generate manifest
    sealedSecretManifest, err := createSealedSecret(d, m.(*kubectl.KubeProvider))
    if err != nil {
        return diag.FromErr(err)
    }

    // 2. Apply against cluster
    exponentialBackoffConfig := backoff.NewExponentialBackOff()
    exponentialBackoffConfig.InitialInterval = 3 * time.Second
    exponentialBackoffConfig.MaxInterval = 30 * time.Second

    if kubectlApplyRetryCount > 0 {
        retryConfig := backoff.WithMaxRetries(exponentialBackoffConfig, kubectlApplyRetryCount)
        retryErr := backoff.Retry(func() error {
            resourceId, err := kubectl.ResourceKubectlManifestApply(ctx, sealedSecretManifest, true, m.(*kubectl.KubeProvider))
            if err != nil {
                log.Printf("[ERROR] creating manifest failed: %+v", err)
            }

            d.Set("manifest", sealedSecretManifest)
            d.SetId(resourceId)
            return err
        }, retryConfig)

        if retryErr != nil {
            return diag.FromErr(retryErr)
        }
    } else {
        resourceId, err := kubectl.ResourceKubectlManifestApply(ctx, sealedSecretManifest, true, m.(*kubectl.KubeProvider))
        if (err != nil) {
            return diag.FromErr(err)
        }

        d.Set("manifest", sealedSecretManifest)
        d.SetId(resourceId)
    }

    // 3. Call read
	return resourceSecretRead(ctx, d, m)
}

func resourceSecretDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	log.Printf("resourceSecretDelete")

	manifest := d.Get("manifest").(string)
    if manifest == "" {
        return nil
    }

    if err := kubectl.ResourceKubectlManifestDelete(ctx, manifest, true, m.(*kubectl.KubeProvider)); err != nil {
        return diag.FromErr(err)
    }

	return nil
}

func resourceSecretRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	log.Printf("resourceSecretRead")

    manifest := d.Get("manifest").(string)

    if manifest == "" {
        return nil
    }

    isGone, err := kubectl.ResourceKubectlManifestRead(ctx, manifest, m);
    if err != nil {
        return diag.FromErr(err)
    }

    if isGone {
        d.SetId("")
    }

	return nil
}

func resourceSecretUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	log.Printf("resourceSecretUpdate")

    if d.HasChange("name") {
        if err := resourceSecretDelete(ctx, d, m); err != nil {
            return err
        }
    }

    if d.HasChange("name") || d.HasChange("secrets")  {
        return resourceSecretCreate(ctx, d, m)
    }

	return nil
}

func createSealedSecret(d *schema.ResourceData, kubeProvider *kubectl.KubeProvider) (string, error) {
	log.Printf("createSealedSecret")

	secrets := d.Get("secrets").(map[string]interface {})
	name := d.Get("name").(string)
	namespace := d.Get("namespace").(string)

    secretsBase64 := map[string]interface{}{}
    for key, value := range secrets {
        strValue := fmt.Sprintf("%v", value)
        secretsBase64[key] = b64.StdEncoding.EncodeToString([]byte(strValue))
    }

    secretManifest, err := utils.GenerateSecretManifest(name, namespace, secretsBase64)
	if err != nil {
		return "", err
	}

	controllerName := d.Get("controller_name").(string)
	controllerNamespace := d.Get("controller_namespace").(string)

    rawCertificate, err := kubeseal.FetchCertificate(controllerName, controllerNamespace, kubeProvider)
	if err != nil {
		return "", err
	}
	defer rawCertificate.Close()

    publicKey, err := kubeseal.ParseKey(rawCertificate)
	if err != nil {
		return "", err
	}

    sealedSecretManifest, err := kubeseal.Seal(secretManifest, publicKey, 0, false)
    if err != nil {
        return "", err
    }

    return sealedSecretManifest, nil
}
