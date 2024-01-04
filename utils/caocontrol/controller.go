package caocontrol

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	routev1 "github.com/openshift/api/route/v1"
	"go.uber.org/zap"
	"gopkg.in/yaml.v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ser_yaml "k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8s_yaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
)

type ControllerOptions struct {
	Logger         *zap.Logger
	KubeConfigPath string
}

type Controller struct {
	logger        *zap.Logger
	Config        *rest.Config
	K8sClient     *kubernetes.Clientset
	DynamicClient *dynamic.DynamicClient
	HostDomain    string
}

// NewController sets up k8s client and config for further cao usage.
func NewController(opts *ControllerOptions) (*Controller, error) {
	// Setup the k8s client for talking to cluster
	restConfig, err := getK8sRestConfig(opts.KubeConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to build config from kubeconfig filepath: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	domain, err := getHostDomain(restConfig)
	if err != nil {
		return nil, fmt.Errorf("error retrieving cluster host domain: %v", err)
	}

	controller := &Controller{logger: opts.Logger,
		Config:        restConfig,
		K8sClient:     clientset,
		DynamicClient: dynamicClient,
		HostDomain:    domain,
	}

	return controller, nil
}

func (c *Controller) NeedGhcrAccess(ghcrUser, ghcrToken string) bool {
	return ghcrUser != "" && ghcrToken != ""
}

func (c *Controller) InstallCRD(crdPath string) error {
	clientSet, err := clientset.NewForConfig(c.Config)
	if err != nil {
		return fmt.Errorf("failed to create clientset object: %v", err)
	}

	// check if exists
	crds, err := clientSet.ApiextensionsV1().CustomResourceDefinitions().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list existing CRDs, if any: %v", err)
	}

	// delete, if exists
	deletePolicy := metav1.DeletePropagationForeground
	for i := range crds.Items {
		crd := &crds.Items[i]

		if crd.Spec.Group == "couchbase.com" {
			if err := clientSet.ApiextensionsV1().CustomResourceDefinitions().Delete(context.Background(), crd.Name, metav1.DeleteOptions{
				PropagationPolicy: &deletePolicy,
			}); err != nil {
				return fmt.Errorf("failed to delete CRD: %v", err)
			}
		}
	}

	crdsRaw, err := os.ReadFile(crdPath)
	if err != nil {
		return fmt.Errorf("failed to open file: %v", err)
	}

	crdYAMLs := strings.Split(string(crdsRaw), "\n---\n")

	for _, crdYAML := range crdYAMLs {
		if strings.TrimSpace(crdYAML) == "" {
			continue
		}

		crd := &apiextensionsv1.CustomResourceDefinition{}
		err = k8s_yaml.NewYAMLOrJSONDecoder(strings.NewReader(crdYAML), 1024).Decode(&crd)
		if err != nil {
			return err
		}

		if _, err := clientSet.ApiextensionsV1().CustomResourceDefinitions().Create(context.Background(), crd, metav1.CreateOptions{}); err != nil {
			return err
		}

		if err = WaitForConditionFuncMet(func(ctx context.Context) (bool, error) {
			crd, err = clientSet.ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), crd.Name, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			for _, cond := range crd.Status.Conditions {
				if cond.Type == apiextensionsv1.Established && cond.Status == apiextensionsv1.ConditionTrue {
					return true, nil
				}
			}
			return false, err
		}, time.Minute); err != nil {
			return err
		}

		if err = WaitForConditionFuncMet(func(ctx context.Context) (bool, error) {
			crd, err = clientSet.ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), crd.Name, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			for _, cond := range crd.Status.Conditions {
				if cond.Type == apiextensionsv1.NamesAccepted && cond.Status == apiextensionsv1.ConditionTrue {
					return true, nil
				}
			}
			return false, err
		}, time.Minute); err != nil {
			return err
		}
	}

	return nil
}

func (c *Controller) createAdmission(version, namespace, caoBinPath string) error {
	args := []string{
		"create",
		"admission",
		"--image=" + AdmissionImageName + ":" + version,
		"--namespace=" + namespace,
		"--image-pull-policy=Always",
	}

	output, err := exec.Command(caoBinPath, args...).CombinedOutput()
	if err != nil {
		c.logger.Info(string(output))
		return err
	}

	// verify admission controller is deployed
	if err = WaitForConditionFuncMet(func(ctx context.Context) (bool, error) {
		admission, err := c.K8sClient.AppsV1().Deployments(namespace).Get(context.TODO(), AdmissionResourceName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		for _, cond := range admission.Status.Conditions {
			if cond.Type == appsv1.DeploymentAvailable && cond.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, err
	}, time.Minute); err != nil {
		return err
	}

	return nil
}

func (c *Controller) deleteAdmission(version, caoBinPath string) error {
	args := []string{
		"delete",
		"admission",
		"--namespace=" + version,
	}

	output, err := exec.Command(caoBinPath, args...).CombinedOutput()
	if err != nil {
		c.logger.Info(string(output))
		return err
	}

	// verify admission controller is undeployed
	if err = WaitForConditionFuncMet(func(ctx context.Context) (bool, error) {
		_, err := c.K8sClient.AppsV1().Deployments(version).Get(context.TODO(), AdmissionResourceName, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}, time.Minute); err != nil {
		return err
	}

	return nil
}

func (c *Controller) CreateAdmission(admissionVer, admissionNamespace, caoBinPath string) error {
	// check if admission controller exists of same version
	admission, err := c.K8sClient.AppsV1().Deployments(admissionNamespace).Get(context.TODO(), AdmissionResourceName, metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return err
	}

	if err != nil && errors.IsNotFound(err) {
		// doesn't exist; create new
		return c.createAdmission(admissionVer, admissionNamespace, caoBinPath)
	}

	if admission != nil {
		existingVer, err := getImageVersionFromDeployment(admission)
		if err != nil {
			return err
		}

		if existingVer != admissionVer {
			err := c.deleteAdmission(admissionNamespace, caoBinPath)
			if err != nil {
				return err
			}
		}
		return c.createAdmission(admissionVer, admissionNamespace, caoBinPath)
	}

	return nil
}

func (c *Controller) createOperator(version, namespace, caoBinPath string) error {
	args := []string{
		"create",
		"operator",
		"--image=" + OperatorImageName + ":" + version,
		"--namespace=" + namespace,
		"--image-pull-policy=Always",
	}

	output, err := exec.Command(caoBinPath, args...).CombinedOutput()
	if err != nil {
		c.logger.Info(string(output))
		return err
	}

	// verify operator is deployed
	if err = WaitForConditionFuncMet(func(ctx context.Context) (bool, error) {
		operator, err := c.K8sClient.AppsV1().Deployments(namespace).Get(context.TODO(), OperatorResourceName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		for _, cond := range operator.Status.Conditions {
			if cond.Type == appsv1.DeploymentAvailable && cond.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, err
	}, time.Minute); err != nil {
		return err
	}

	return nil
}

func (c *Controller) deleteOperator(version, caoBinPath string) error {
	args := []string{
		"delete",
		"operator",
		"--namespace=" + version,
	}

	output, err := exec.Command(caoBinPath, args...).CombinedOutput()
	if err != nil {
		c.logger.Info(string(output))
		return err
	}

	// verify operator is undeployed
	if err = WaitForConditionFuncMet(func(ctx context.Context) (bool, error) {
		_, err := c.K8sClient.AppsV1().Deployments(version).Get(context.TODO(), OperatorResourceName, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}, time.Minute); err != nil {
		return err
	}

	return nil
}

func (c *Controller) CreateOperator(operatorVer, operatorNamespace, caoBinPath string) error {
	// check if operator exists of same version
	operator, err := c.K8sClient.AppsV1().Deployments(operatorNamespace).Get(context.TODO(), OperatorResourceName, metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return err
	}

	if err != nil && errors.IsNotFound(err) {
		// doesn't exist; create new
		return c.createOperator(operatorVer, operatorNamespace, caoBinPath)
	}

	if operator != nil {
		existingVer, err := getImageVersionFromDeployment(operator)
		if err != nil {
			return err
		}

		if existingVer != operatorVer {
			err := c.deleteOperator(operatorNamespace, caoBinPath)
			if err != nil {
				return err
			}
		}
		return c.createOperator(operatorVer, operatorNamespace, caoBinPath)
	}

	return nil
}

func (c *Controller) CreateGhcrSecret(ghcrUser, ghcrToken, operatorNamespace string) error {
	// base64 encoding username and password..
	auth := ghcrUser + ":" + ghcrToken
	auth = base64.StdEncoding.EncodeToString([]byte(auth))

	data := `{"auths":{"` + "ghcr.io" + `":{"auth":"` + auth + `"}}}`

	// Create the secret object
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      GhcrK8sSecretName,
			Namespace: operatorNamespace, // bc all cbc resources/subresources are managed in operator namespace
		},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(data),
		},
		Type: corev1.SecretTypeDockerConfigJson,
	}

	return createK8sSecret(c.K8sClient, secret, operatorNamespace)
}

func (c *Controller) CreateCbcAdminSecret(operatorNamespace string) error {
	// Create the secret object
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      CbdcCbcAdminSecretName,
			Namespace: operatorNamespace, // bc all cbc resources/subresources are managed in operator namespace
		},
		Data: map[string][]byte{
			corev1.BasicAuthUsernameKey: []byte("Administrator"),
			corev1.BasicAuthPasswordKey: []byte("password"),
		},
		Type: corev1.SecretTypeOpaque,
	}

	return createK8sSecret(c.K8sClient, secret, operatorNamespace)
}

func createCbcK8sResource(cbcc *cbcConfig) (schema.GroupVersionResource, *unstructured.Unstructured, error) {
	yamlData, err := yaml.Marshal(cbcc)
	if err != nil {
		return schema.GroupVersionResource{}, nil, err
	}

	decoder := ser_yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)
	obj := &unstructured.Unstructured{}
	_, gvk, err := decoder.Decode(yamlData, nil, obj)
	if err != nil {
		return schema.GroupVersionResource{}, nil, err
	}
	gvr := schema.GroupVersionResource{
		Group:    gvk.Group,
		Version:  gvk.Version,
		Resource: strings.ToLower(gvk.Kind + "s"),
	}
	return gvr, obj, nil
}

func (c *Controller) CreateCouchbaseCluster(cbcc *cbcConfig, namespace string) error {
	gvr, obj, err := createCbcK8sResource(cbcc)
	if err != nil {
		return fmt.Errorf("failed to transform to k8s resource: %w", err)
	}

	// create cbc resource
	err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		_, err = c.DynamicClient.Resource(gvr).Namespace(namespace).Create(context.TODO(), obj, metav1.CreateOptions{})
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to apply YAML configuration: %v", err)
	}

	if cbcc.Spec.Networking.CloudNativeGateway.Image != "" {
		// check if the cng service is created
		if err = WaitForConditionFuncMet(func(ctx context.Context) (bool, error) {
			_, err := c.K8sClient.CoreV1().Services(namespace).Get(context.TODO(), cbcc.Metadata.Name+CNGServiceNamePrefix, metav1.GetOptions{})
			if errors.IsNotFound(err) {
				return false, nil
			} else if err != nil {
				return false, err
			}
			return true, nil
		}, 10*time.Minute); err != nil {
			return err
		}
	}

	return nil
}

func (c *Controller) ListCouchbaseClusters(namespace string) ([]string, error) {
	gvr := schema.GroupVersionResource{
		Group:    "couchbase.com",
		Version:  "v2",
		Resource: "couchbaseclusters",
	}
	
	clusterIDs := []string{}

	// fetch all cbc resources
	list, err := c.DynamicClient.Resource(gvr).Namespace(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return clusterIDs, fmt.Errorf("failed to retrieve couchbase clusters: %v", err)
	}

	for _, item := range list.Items {
		metadata, found, err := unstructured.NestedMap(item.Object, "metadata")
		if err != nil || !found {
			continue
		}
		clusterIDs = append(clusterIDs, metadata["name"].(string))
	}

	return clusterIDs, nil
}

func (c *Controller) CreateExternalAccess(serviceName, namespace string) error {
	route := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      OpenshiftRouteName,
			Namespace: namespace,
		},
		Spec: routev1.RouteSpec{
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: serviceName,
			},
			TLS: &routev1.TLSConfig{
				Termination: routev1.TLSTerminationPassthrough,
			},
			Port: &routev1.RoutePort{
				TargetPort: intstr.FromInt(443),
			},
		},
	}

	return createOcRoute(c.Config, route, namespace)
}

func (c *Controller) CreateCNGTLSSecret(domain, secretName, namespace string) error {
	cert, key, err := generateX509Certificate(domain)
	if err != nil {
		return fmt.Errorf("error generating x509 certificate: %v", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			corev1.TLSCertKey:       cert,
			corev1.TLSPrivateKeyKey: key,
		},
		Type: corev1.SecretTypeTLS,
	}

	return createK8sSecret(c.K8sClient, secret, namespace)
}


func (c *Controller) GetRouteURL(namespace string, clusterID string) (string, error) {
	routes, err := getOcRoutes(c.Config, namespace)
	if err != nil {
		return "", err
	}

	var hostPort string
	for _, route := range routes.Items {
		if strings.Contains(route.Spec.To.Name, clusterID) {
			hostPort = route.Spec.Host
			break
		}
	}

	return hostPort, nil
}