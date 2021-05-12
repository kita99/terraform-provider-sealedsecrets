package sealedsecrets

import (
	"fmt"
	"os"

	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"

	"github.com/kita99/terraform-provider-sealedsecrets/utils"
)

func resourceSecret() *schema.Resource {
	return &schema.Resource{
		Create: resourceSecretCreate,
		Read:   resourceSecretRead,
		Update: resourceSecretUpdate,
		Delete: resourceSecretDelete,

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
				Description: "Name of the Kubeseal controller in the cluster",
			},
			"controller_namespace": &schema.Schema{
				Type:        schema.TypeString,
				Required:    true,
				Description: "Namespace of the Kubeseal controller in the cluster",
			},
		},
	}
}

func resourceSecretCreate(d *schema.ResourceData, m interface{}) error {
	utils.Log("resourceSecretCreate")

	mainCmd := m.(*Cmd)
    sealedSecretPath := "/tmp/sealedsecret.yaml"

    if err := createSealedSecret(d, mainCmd, sealedSecretPath); err != nil {
        return err
    }
	utils.Log(fmt.Sprintf("Sealed secret (%s) has been created\n", sealedSecretPath))

	if err := utils.ExecuteCmd(mainCmd.kubectl, "apply", "-f", sealedSecretPath); err != nil {
		return err
	}

	d.SetId(utils.SHA256(utils.GetFileContent(sealedSecretPath)))

	return resourceSecretRead(d, m)
}

func resourceSecretDelete(d *schema.ResourceData, m interface{}) error {
	utils.Log("resourceSecretDelete")

	mainCmd := m.(*Cmd)

	name := d.Get("name").(string)
	ns := d.Get("namespace").(string)

	if err := utils.ExecuteCmd(mainCmd.kubectl, "delete", "SealedSecret", name, "-n", ns); err != nil {
		utils.Log("Failed to delete sealed secret: " + name)
	}

	d.SetId("")

	return nil
}

func resourceSecretRead(d *schema.ResourceData, m interface{}) error {
	utils.Log("resourceSecretRead")

    name := d.Get("name").(string)
    ns := d.Get("namespace").(string)

	mainCmd := m.(*Cmd)
	if err := utils.ExecuteCmd(mainCmd.kubectl, "get", "SealedSecret", name, "-n", ns); err != nil {
		d.SetId("")
        return nil
	}

	return nil
}

func resourceSecretUpdate(d *schema.ResourceData, m interface{}) error {
	utils.Log("resourceSecretUpdate")

	mainCmd := m.(*Cmd)

	name := d.Get("name").(string)
	ns := d.Get("namespace").(string)

	if err := utils.ExecuteCmd(mainCmd.kubectl, "delete", "SealedSecret", name, "-n", ns); err != nil {
		utils.Log(fmt.Sprintf("Failed to delete SealedSecret: %s.%s\n", ns, name))
	}

	return resourceSecretCreate(d, m)
}

func shouldCreateSealedSecret(d *schema.ResourceData) bool {
    // TODO: Implement me
	return true
}

func createSealedSecret(d *schema.ResourceData, mainCmd *Cmd, sealedSecretPath string) error {
	secrets := d.Get("secrets").(string)

	ssDir := utils.GetDir(sealedSecretPath)
	if !utils.PathExists(ssDir) {
		utils.Log(fmt.Sprintf("Sealed secret directory (%s) doesn't exist\n", ssDir))
		os.Mkdir(ssDir, os.ModePerm)
	}
	utils.Log(fmt.Sprintf("Sealed secret (%s) has been created\n", ssDir))

	name := d.Get("name").(string)
	ns := d.Get("namespace").(string)
	sType := d.Get("type").(string)
	controllerName := d.Get("controller_name").(string)
	controllerNamespace := d.Get("controller_namespace").(string)

	nsArg := fmt.Sprintf("%s=%s", "--namespace", ns)
	typeArg := fmt.Sprintf("%s=%s", "--type", sType)

    fromLiteralArg := ""
    for key, value := range secrets {
        fromLiteralArg += fmt.Sprintf("%s=%s=%s ", "--from-literal", key, value)
    }

	dryRunArg := "--dry-run=client"
	outputArg := fmt.Sprintf("%s=%s", "--output", "yaml")

    controllerNameArg := fmt.Sprintf("%s=%s", "--controller-name", controllerName)
    controllerNamespaceArg := fmt.Sprintf("%s=%s", "--controller-namespace", controllerNamespace)
	fetchCertArg := "--fetch-cert"
	formatArg := fmt.Sprintf("%s %s", "--format", "yaml")

	return utils.ExecuteCmd(
        mainCmd.kubectl, "create", "secret", "generic", name, nsArg, typeArg, fromLiteralArg, dryRunArg, outputArg,
        "|",
        mainCmd.kubeseal, controllerNameArg, controllerNamespaceArg, fetchCertArg, formatArg, ">", sealedSecretPath)
}
