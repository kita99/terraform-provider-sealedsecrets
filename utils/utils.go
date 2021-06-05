package utils

import (
    "io"
	"crypto/sha256"
	"fmt"
	"bytes"
    "text/template"
)

var (
    secretManifestTemplate = `
apiVersion: v1
data:
  {{- range $key, $value := .Secrets }}
  {{ $key }}: {{ $value -}}
  {{ end }}
kind: Secret
metadata:
  creationTimestamp: null
  name: {{ .Name }}
  namespace: {{ .Namespace }}
type: {{ .Type }}`
)

type SecretManifest struct {
    Name string
    Namespace string
    Type string
    Secrets map[string]interface {}
}

func SHA256(src string) string {
	h := sha256.New()
	h.Write([]byte(src))
	return fmt.Sprintf("%x", h.Sum(nil))
}

func GenerateSecretManifest(name string, namespace string, _type string, secrets map[string]interface {}) (io.Reader, error) {
    secretManifestYAML := new(bytes.Buffer)

    secretManifest := SecretManifest{
        Name: name,
        Namespace: namespace,
        Type: _type,
        Secrets: secrets,
    }

    t := template.Must(template.New("secretManifestTemplate").Parse(secretManifestTemplate))
    err := t.Execute(secretManifestYAML, secretManifest)
	if err != nil {
		return nil, err
	}

    return secretManifestYAML, nil
}

func ExpandStringSlice(s []interface{}) []string {
	result := make([]string, len(s), len(s))
	for k, v := range s {
		// Handle the Terraform parser bug which turns empty strings in lists to nil.
		if v == nil {
			result[k] = ""
		} else {
			result[k] = v.(string)
		}
	}
	return result
}

