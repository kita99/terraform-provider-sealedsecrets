package kubectl

import (
	"context"
	"encoding/json"
	"fmt"
    "regexp"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"io/ioutil"
	"k8s.io/cli-runtime/pkg/printers"
	"os"
	"time"
	"log"
	"strings"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	k8sresource "k8s.io/cli-runtime/pkg/resource"
	apiregistration "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	"k8s.io/kubectl/pkg/cmd/apply"
	k8sdelete "k8s.io/kubectl/pkg/cmd/delete"
    diskcached "k8s.io/client-go/discovery/cached/disk"

	"github.com/icza/dyno"

	yamlParser "gopkg.in/yaml.v2"
	"k8s.io/apimachinery/pkg/api/errors"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	meta_v1_unstruct "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
	yamlWriter "sigs.k8s.io/yaml"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
    aggregator "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset"
    "k8s.io/client-go/rest"
    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/tools/clientcmd"
    "k8s.io/apimachinery/pkg/api/meta"
    "path/filepath"
    "k8s.io/client-go/restmapper"
    "github.com/mitchellh/go-homedir"
)

type KubeProvider struct {
	MainClientset       *kubernetes.Clientset
	RestConfig          rest.Config
	AggregatorClientset *aggregator.Clientset
}

func (p *KubeProvider) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return nil
}

func (p *KubeProvider) ToRESTConfig() (*rest.Config, error) {
	return &p.RestConfig, nil
}

func (p *KubeProvider) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	home, _ := homedir.Dir()
	var httpCacheDir = filepath.Join(home, ".kube", "http-cache")

	discoveryCacheDir := computeDiscoverCacheDir(filepath.Join(home, ".kube", "cache", "discovery"), p.RestConfig.Host)
	return diskcached.NewCachedDiscoveryClientForConfig(&p.RestConfig, discoveryCacheDir, httpCacheDir, 10*time.Minute)
}

func (p *KubeProvider) ToRESTMapper() (meta.RESTMapper, error) {
	discoveryClient, _ := p.ToDiscoveryClient()
	if discoveryClient != nil {
		mapper := restmapper.NewDeferredDiscoveryRESTMapper(discoveryClient)
		expander := restmapper.NewShortcutExpander(mapper, discoveryClient)
		return expander, nil
	}

	return nil, fmt.Errorf("no restmapper")
}

const (
	// https://github.com/kubernetes/kubernetes/blob/master/pkg/controller/deployment/util/deployment_util.go#L93
	TimedOutReason = "ProgressDeadlineExceeded"
)

type UnstructuredManifest struct {
	unstruct *meta_v1_unstruct.Unstructured
}

func (m *UnstructuredManifest) hasNamespace() bool {
	ns := m.unstruct.GetNamespace()
	return ns != ""
}

func (m *UnstructuredManifest) String() string {
	if m.hasNamespace() {
		return fmt.Sprintf("%s/%s", m.unstruct.GetNamespace(), m.unstruct.GetName())
	}

	return m.unstruct.GetName()
}

func ResourceKubectlManifestApply(ctx context.Context, yaml string, waitForRolout bool, kubeProvider *KubeProvider) (string, error) {
	manifest, err := parseYaml(yaml)
	if err != nil {
		return "", fmt.Errorf("failed to parse kubernetes resource: %+v", err)
	}

	log.Printf("[DEBUG] %v apply kubernetes resource:\n%s", manifest, yaml)

	// Create a client to talk to the resource API based on the APIVersion and Kind
	// defined in the YAML
	restClient := getRestClientFromUnstructured(manifest, kubeProvider)
	if restClient.Error != nil {
		return "", fmt.Errorf("%v failed to create kubernetes rest client for update of resource: %+v", manifest, restClient.Error)
	}

	// Update the resource in Kubernetes, using a temp file
	yamlJson, err := manifest.unstruct.MarshalJSON()
	if err != nil {
		return "", fmt.Errorf("%v failed to convert object to json: %+v", manifest, err)
	}

	yamlParsed, err := yamlWriter.JSONToYAML(yamlJson)
	if err != nil {
		return "", fmt.Errorf("%v failed to convert json to yaml: %+v", manifest, err)
	}

	yaml = string(yamlParsed)

	tmpfile, _ := ioutil.TempFile("", "*kubectl_manifest.yaml")
	_, _ = tmpfile.Write([]byte(yaml))
	_ = tmpfile.Close()

	applyOptions := apply.NewApplyOptions(genericclioptions.IOStreams{
		In:     strings.NewReader(yaml),
		Out:    log.Writer(),
		ErrOut: log.Writer(),
	})
	applyOptions.Builder = k8sresource.NewBuilder(k8sresource.RESTClientGetter(kubeProvider))
	applyOptions.DeleteOptions = &k8sdelete.DeleteOptions{
		FilenameOptions: k8sresource.FilenameOptions{
			Filenames: []string{tmpfile.Name()},
		},
	}

	applyOptions.ToPrinter = func(string) (printers.ResourcePrinter, error) {
		return printers.NewDiscardingPrinter(), nil
	}

	if manifest.hasNamespace() {
		applyOptions.Namespace = manifest.unstruct.GetNamespace()
	}

	log.Printf("[INFO] %s perform apply of manifest", manifest)

	err = applyOptions.Run()
	_ = os.Remove(tmpfile.Name())
	if err != nil {
		return "", fmt.Errorf("%v failed to run apply: %+v", manifest, err)
	}

	log.Printf("[INFO] %v manifest applied, fetch resource from kubernetes", manifest)

	// get the resource from Kubernetes
	response, err := restClient.ResourceInterface.Get(ctx, manifest.unstruct.GetName(), meta_v1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("%v failed to fetch resource from kubernetes: %+v", manifest, err)
	}

	// get selfLink or generate (for Kubernetes 1.20+)
	selfLink := response.GetSelfLink()
	if len(selfLink) == 0 {
		selfLink = generateSelfLink(
			response.GetAPIVersion(),
			response.GetNamespace(),
			response.GetKind(),
			response.GetName())
	}

	log.Printf("[DEBUG] %v fetched successfully, set id to: %v", manifest, selfLink)

    return selfLink, nil
}

func ResourceKubectlManifestRead(ctx context.Context, yaml string, meta interface{}) (bool, error) {
	manifest, err := parseYaml(yaml)
	if err != nil {
		return false, fmt.Errorf("failed to parse kubernetes resource: %+v", err)
	}

	// if overrideNamespace, ok := d.GetOk("override_namespace"); ok {
	// 	manifest.unstruct.SetNamespace(overrideNamespace.(string))
	// }

	// Create a client to talk to the resource API based on the APIVersion and Kind
	// defined in the YAML
	restClient := getRestClientFromUnstructured(manifest, meta.(*KubeProvider))
	if restClient.Status == RestClientInvalidTypeError {
		log.Printf("[WARN] kubernetes resource has an invalid type, removing from state")
		// d.SetId("")
		return false, fmt.Errorf("kubernetes resource has an invalid type")
	}

	if restClient.Error != nil {
		return false, fmt.Errorf("failed to create kubernetes rest client for read of resource: %+v", restClient.Error)
	}

	return resourceKubectlManifestReadUsingClient(ctx, meta, restClient.ResourceInterface, manifest)
}

func resourceKubectlManifestReadUsingClient(ctx context.Context, meta interface{}, client dynamic.ResourceInterface, manifest *UnstructuredManifest) (bool, error) {

	log.Printf("[DEBUG] %v fetch from kubernetes", manifest)

	// Get the resource from Kubernetes
	metaObjLive, err := client.Get(ctx, manifest.unstruct.GetName(), meta_v1.GetOptions{})
	resourceGone := errors.IsGone(err) || errors.IsNotFound(err)
	if resourceGone {
		log.Printf("[WARN] kubernetes resource not found, removing from state")
		// d.SetId("")
		return true, nil
	}

	if err != nil {
		return false, fmt.Errorf("%v failed to get resource from kubernetes: %+v", manifest, err)
	}

	if metaObjLive.GetUID() == "" {
		return false, fmt.Errorf("%v failed to parse item and get UUID: %+v", manifest, metaObjLive)
	}

	// Capture the UID and Resource_version from the cluster at the current time
	// _ = d.Set("live_uid", metaObjLive.GetUID())
	// _ = d.Set("live_resource_version", metaObjLive.GetResourceVersion())

	// comparisonOutput, err := getLiveManifestFilteredForUserProvidedOnly(d, manifest.unstruct, metaObjLive)
	// if err != nil {
	// 	return fmt.Errorf("%v failed to compare maps of manifest vs version in kubernetes: %+v", manifest, err)
	// }

	// _ = d.Set("live_manifest_incluster", comparisonOutput)

	return false, nil
}

func ResourceKubectlManifestDelete(ctx context.Context, yaml string, wait bool, kubeProvider *KubeProvider) error {
	manifest, err := parseYaml(yaml)
	if err != nil {
		return fmt.Errorf("failed to parse kubernetes resource: %+v", err)
	}

	log.Printf("[DEBUG] %v delete kubernetes resource:\n%s", manifest, yaml)

	restClient := getRestClientFromUnstructured(manifest, kubeProvider)
	if restClient.Error != nil {
		return fmt.Errorf("%v failed to create kubernetes rest client for delete of resource: %+v", manifest, restClient.Error)
	}

	log.Printf("[INFO] %s perform delete of manifest", manifest)

	propagationPolicy := meta_v1.DeletePropagationBackground
	if wait {
		propagationPolicy = meta_v1.DeletePropagationForeground
	}
	err = restClient.ResourceInterface.Delete(ctx, manifest.unstruct.GetName(), meta_v1.DeleteOptions{PropagationPolicy: &propagationPolicy})
	resourceGone := errors.IsGone(err) || errors.IsNotFound(err)
	if err != nil && !resourceGone {
		return fmt.Errorf("%v failed to delete kubernetes resource: %+v", manifest, err)
	}

	return nil
}

// To make things play nice we need the JSON representation of the object as the `RawObj`
// 1. UnMarshal YAML into map
// 2. Marshal map into JSON
// 3. UnMarshal JSON into the Unstructured type so we get some K8s checking
func parseYaml(yaml string) (*UnstructuredManifest, error) {
	rawYamlParsed := &map[string]interface{}{}
	err := yamlParser.Unmarshal([]byte(yaml), rawYamlParsed)
	if err != nil {
		return nil, err
	}

	rawJSON, err := json.Marshal(dyno.ConvertMapI2MapS(*rawYamlParsed))
	if err != nil {
		return nil, err
	}

	unstruct := meta_v1_unstruct.Unstructured{}
	err = unstruct.UnmarshalJSON(rawJSON)
	if err != nil {
		return nil, err
	}

	manifest := &UnstructuredManifest{
		unstruct: &unstruct,
	}

	log.Printf("[DEBUG] %s Unstructed YAML: %+v\n", manifest, manifest.unstruct.UnstructuredContent())
	return manifest, nil
}

type RestClientStatus int

const (
	RestClientOk = iota
	RestClientGenericError
	RestClientInvalidTypeError
)

type RestClientResult struct {
	ResourceInterface dynamic.ResourceInterface
	Error             error
	Status            RestClientStatus
}

func RestClientResultSuccess(resourceInterface dynamic.ResourceInterface) *RestClientResult {
	return &RestClientResult{
		ResourceInterface: resourceInterface,
		Error:             nil,
		Status:            RestClientOk,
	}
}

func RestClientResultFromErr(err error) *RestClientResult {
	return &RestClientResult{
		ResourceInterface: nil,
		Error:             err,
		Status:            RestClientGenericError,
	}
}

func RestClientResultFromInvalidTypeErr(err error) *RestClientResult {
	return &RestClientResult{
		ResourceInterface: nil,
		Error:             err,
		Status:            RestClientInvalidTypeError,
	}
}

func getRestClientFromUnstructured(manifest *UnstructuredManifest, provider *KubeProvider) *RestClientResult {

	doGetRestClientFromUnstructured := func(manifest *UnstructuredManifest, provider *KubeProvider) *RestClientResult {
		// Use the k8s Discovery service to find all valid APIs for this cluster
		discoveryClient, _ := provider.ToDiscoveryClient()
		var resources []*meta_v1.APIResourceList
		var err error
		_, resources, err = discoveryClient.ServerGroupsAndResources()

		// There is a partial failure mode here where not all groups are returned `GroupDiscoveryFailedError`
		// we'll try and continue in this condition as it's likely something we don't need
		// and if it is the `checkAPIResourceIsPresent` check will fail and stop the process
		if err != nil && !discovery.IsGroupDiscoveryFailedError(err) {
			return RestClientResultFromErr(err)
		}

		// Validate that the APIVersion provided in the YAML is valid for this cluster
		apiResource, exists := checkAPIResourceIsPresent(resources, *manifest.unstruct)
		if !exists {
			// api not found, invalidate the cache and try again
			// this handles the case when a CRD is being created by another kubectl_manifest resource run
			discoveryClient.Invalidate()
			_, resources, err = discoveryClient.ServerGroupsAndResources()

			if err != nil && !discovery.IsGroupDiscoveryFailedError(err) {
				return RestClientResultFromErr(err)
			}

			// check for resource again
			apiResource, exists = checkAPIResourceIsPresent(resources, *manifest.unstruct)
			if !exists {
				return RestClientResultFromInvalidTypeErr(fmt.Errorf("resource [%s/%s] isn't valid for cluster, check the APIVersion and Kind fields are valid", manifest.unstruct.GroupVersionKind().GroupVersion().String(), manifest.unstruct.GetKind()))
			}
		}

		resourceStruct := k8sschema.GroupVersionResource{Group: apiResource.Group, Version: apiResource.Version, Resource: apiResource.Name}
		// For core services (ServiceAccount, Service etc) the group is incorrectly parsed.
		// "v1" should be empty group and "v1" for version
		if resourceStruct.Group == "v1" && resourceStruct.Version == "" {
			resourceStruct.Group = ""
			resourceStruct.Version = "v1"
		}
		client := dynamic.NewForConfigOrDie(&provider.RestConfig).Resource(resourceStruct)

		if apiResource.Namespaced {
			if manifest.unstruct.GetNamespace() == "" {
				manifest.unstruct.SetNamespace("default")
			}
			return RestClientResultSuccess(client.Namespace(manifest.unstruct.GetNamespace()))
		}

		return RestClientResultSuccess(client)
	}

	discoveryWithTimeout := func(manifest *UnstructuredManifest, provider *KubeProvider) <-chan *RestClientResult {
		ch := make(chan *RestClientResult)
		go func() {
			ch <- doGetRestClientFromUnstructured(manifest, provider)
		}()
		return ch
	}

	timeout := time.NewTimer(60 * time.Second)
	defer timeout.Stop()
	select {
	case res := <-discoveryWithTimeout(manifest, provider):
		return res
	case <-timeout.C:
		log.Printf("[ERROR] %v timed out fetching resources from discovery client", manifest)
		return RestClientResultFromErr(fmt.Errorf("%v timed out fetching resources from discovery client", manifest))
	}
}

// checkAPIResourceIsPresent Loops through a list of available APIResources and
// checks there is a resource for the APIVersion and Kind defined in the 'resource'
// if found it returns true and the APIResource which matched
func checkAPIResourceIsPresent(available []*meta_v1.APIResourceList, resource meta_v1_unstruct.Unstructured) (*meta_v1.APIResource, bool) {
	for _, rList := range available {
		if rList == nil {
			continue
		}
		group := rList.GroupVersion
		for _, r := range rList.APIResources {
			if group == resource.GroupVersionKind().GroupVersion().String() && r.Kind == resource.GetKind() {
				r.Group = rList.GroupVersion
				r.Kind = rList.Kind
				return &r, true
			}
		}
	}
	return nil, false
}

func waitForAPIServiceAvailableFunc(ctx context.Context, provider *KubeProvider, name string) resource.RetryFunc {
	return func() *resource.RetryError {

		apiService, err := provider.AggregatorClientset.ApiregistrationV1().APIServices().Get(ctx, name, meta_v1.GetOptions{})
		if err != nil {
			return resource.NonRetryableError(err)
		}

		for i := range apiService.Status.Conditions {
			if apiService.Status.Conditions[i].Type == apiregistration.Available {
				return nil
			}
		}

		return resource.RetryableError(fmt.Errorf("Waiting for APIService %v to be Available", name))
	}
}

// generateSelfLink creates a selfLink of the form:
//     "/apis/<apiVersion>/namespaces/<namespace>/<kind>s/<name>"
//
// The selfLink attribute is not available in Kubernetes 1.20+ so we need
// to generate a consistent, unique ID for our Terraform resources.
func generateSelfLink(apiVersion, namespace, kind, name string) string {
	var b strings.Builder

	// for any v1 api served objects, they used to be served from /api
	// all others are served from /apis
	if apiVersion == "v1" {
		b.WriteString("/api")
	} else {
		b.WriteString("/apis")
	}

	if len(apiVersion) != 0 {
		fmt.Fprintf(&b, "/%s", apiVersion)
	}
	if len(namespace) != 0 {
		fmt.Fprintf(&b, "/namespaces/%s", namespace)
	}
	if len(kind) != 0 {
		var suffix string
		if strings.HasSuffix(kind, "s") {
			suffix = "es"
		} else {
			suffix = "s"
		}
		fmt.Fprintf(&b, "/%s%s", strings.ToLower(kind), suffix)
	}
	if len(name) != 0 {
		fmt.Fprintf(&b, "/%s", name)
	}
	return b.String()
}

// overlyCautiousIllegalFileCharacters matches characters that *might* not be supported.  Windows is really restrictive, so this is really restrictive
var overlyCautiousIllegalFileCharacters = regexp.MustCompile(`[^(\w/\.)]`)

// computeDiscoverCacheDir takes the parentDir and the host and comes up with a "usually non-colliding" name.
func computeDiscoverCacheDir(parentDir, host string) string {
	// strip the optional scheme from host if its there:
	schemelessHost := strings.Replace(strings.Replace(host, "https://", "", 1), "http://", "", 1)
	// now do a simple collapse of non-AZ09 characters.  Collisions are possible but unlikely.  Even if we do collide the problem is short lived
	safeHost := overlyCautiousIllegalFileCharacters.ReplaceAllString(schemelessHost, "_")
	return filepath.Join(parentDir, safeHost)
}
