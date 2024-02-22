package caocontrol

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/pkg/errors"
	"go.uber.org/zap"
	"golang.org/x/exp/slices"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kyaml "k8s.io/apimachinery/pkg/util/yaml"
)

const (
	DefaultAdmissionControllerNamespace = "default"
	DefaultAdmissionControllerName      = "couchbase-operator-admission"
	DefaultOperatorName                 = "couchbase-operator"

	CbdcAdmissionControllerNamespace = "cbdc2-cao-admission"

	GhcrSecretName = "ghcr-secret"
)

type ControllerOptions struct {
	Logger         *zap.Logger
	CaoToolsPath   string
	KubeConfigPath string
	KubeContext    string
	GhcrUser       string
	GhcrToken      string
}

type Controller struct {
	logger         *zap.Logger
	caoToolsPath   string
	kubeConfigPath string
	kubeContext    string
	ghcrUser       string
	ghcrToken      string
	restConfig     *rest.Config
}

// NewController sets up k8s client and config for further cao usage.
func NewController(opts *ControllerOptions) (*Controller, error) {
	kubeConfig, err := clientcmd.LoadFromFile(opts.KubeConfigPath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to load kubeconfig")
	}

	kubeRestConfig, err := clientcmd.NewDefaultClientConfig(*kubeConfig, &clientcmd.ConfigOverrides{
		CurrentContext: opts.KubeContext,
	}).ClientConfig()
	if err != nil {
		return nil, errors.Wrap(err, "failed to create rest config for context")
	}

	return &Controller{
		logger:         opts.Logger,
		caoToolsPath:   opts.CaoToolsPath,
		kubeConfigPath: opts.KubeConfigPath,
		kubeContext:    opts.KubeContext,
		ghcrUser:       opts.GhcrUser,
		ghcrToken:      opts.GhcrToken,
		restConfig:     kubeRestConfig,
	}, nil
}

func waitForFunc(ctx context.Context, fn func(ctx context.Context) (bool, error), maxWait time.Duration) error {
	expiryTime := time.Now().Add(maxWait)

	subCtx, cancel := context.WithDeadline(ctx, expiryTime)
	defer cancel()

	for {
		success, err := fn(subCtx)
		if err != nil {
			return err
		}

		if success {
			return nil
		}

		time.Sleep(500 * time.Millisecond)

		if time.Now().After(expiryTime) {
			return errors.New("timed out waiting for condition")
		}
	}
}

func (c *Controller) caoExecAndPipe(ctx context.Context, logger *zap.Logger, args []string) error {
	caoPath := path.Join(c.caoToolsPath, "bin/cao")

	args = append([]string{
		"--kubeconfig",
		c.kubeConfigPath,
		"--context",
		c.kubeContext,
	}, args...)

	logger.Debug("executing command",
		zap.String("exec", caoPath),
		zap.Strings("args", args))

	outPipeRdr, outPipeWrt := io.Pipe()
	defer outPipeWrt.Close()
	go func() {
		scanner := bufio.NewScanner(outPipeRdr)
		for scanner.Scan() {
			logger.Debug("exec output", zap.String("text", scanner.Text()))
		}
	}()

	errPipeRdr, errPipeWrt := io.Pipe()
	defer errPipeWrt.Close()
	go func() {
		scanner := bufio.NewScanner(errPipeRdr)
		for scanner.Scan() {
			logger.Debug("exec error output", zap.String("text", scanner.Text()))
		}
	}()

	cmd := exec.Command(caoPath, args...)
	cmd.Stdout = outPipeWrt
	cmd.Stderr = errPipeWrt
	return cmd.Run()
}

func (c *Controller) Ping(ctx context.Context) error {
	kubes, err := kubernetes.NewForConfig(c.restConfig)
	if err != nil {
		return errors.Wrap(err, "failed to create kubernetes client")
	}

	_, err = kubes.CoreV1().Pods("default").List(ctx, metav1.ListOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to list pods")
	}

	return nil
}

func (c *Controller) IsOpenShift(ctx context.Context) (bool, error) {
	discCli, err := discovery.NewDiscoveryClientForConfig(c.restConfig)
	if err != nil {
		return false, errors.Wrap(err, "failed to create discovery client")
	}

	serverGroups, err := discCli.ServerGroups()
	if err != nil {
		return false, errors.Wrap(err, "failed to get server groups")
	}

	for _, serverGroup := range serverGroups.Groups {
		if serverGroup.Name == "route.openshift.io" {
			return true, nil
		}
	}

	return false, nil
}

func (c *Controller) IsCrdInstalled(ctx context.Context) (bool, error) {
	clientSet, err := clientset.NewForConfig(c.restConfig)
	if err != nil {
		return false, errors.Wrap(err, "failed to create clientset client")
	}

	crdList, err := clientSet.ApiextensionsV1().CustomResourceDefinitions().List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, errors.Wrap(err, "failed to list crds")
	}

	for _, crd := range crdList.Items {
		if crd.Spec.Group == "couchbase.com" {
			return true, nil
		}
	}

	return false, nil
}

func (c *Controller) InstallDefaultCrd(ctx context.Context) error {
	clientSet, err := clientset.NewForConfig(c.restConfig)
	if err != nil {
		return errors.Wrap(err, "failed to create clientset client")
	}

	c.logger.Info("removing all existing couchbase crds")

	crdList, err := clientSet.ApiextensionsV1().CustomResourceDefinitions().List(ctx, metav1.ListOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to list crds")
	}

	for _, crd := range crdList.Items {
		if crd.Spec.Group == "couchbase.com" {
			err := clientSet.ApiextensionsV1().CustomResourceDefinitions().Delete(context.Background(), crd.Name, metav1.DeleteOptions{
				PropagationPolicy: ptr.To(metav1.DeletePropagationForeground),
			})
			if err != nil {
				return errors.Wrap(err, "failed to delete crd")
			}
		}
	}

	c.logger.Info("installing couchbase crds")

	crdFile, err := os.OpenFile(path.Join(c.caoToolsPath, "crd.yaml"), os.O_RDONLY, 0)
	if err != nil {
		return errors.Wrap(err, "failed to load cao-tools crd.yaml")
	}

	crdReader := kyaml.NewYAMLOrJSONDecoder(crdFile, 1024)

	var installedCrds []string
	for {
		crd := &apiextensionsv1.CustomResourceDefinition{}
		err := crdReader.Decode(&crd)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return errors.Wrap(err, "failed to decode crd block")
		}

		_, err = clientSet.ApiextensionsV1().CustomResourceDefinitions().Create(context.Background(), crd, metav1.CreateOptions{})
		if err != nil {
			return errors.Wrap(err, "failed to install crd")
		}

		installedCrds = append(installedCrds, crd.Name)
	}

	c.logger.Info("waiting for couchbase crds to apply")

	err = waitForFunc(ctx, func(ctx context.Context) (bool, error) {
		for _, crdName := range installedCrds {
			crd, err := clientSet.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, crdName, metav1.GetOptions{})
			if err != nil {
				return false, errors.Wrap(err, "failed to read crd state")
			}

			crdEstablished := false
			namesAccepted := false
			for _, cond := range crd.Status.Conditions {
				if cond.Type == apiextensionsv1.Established && cond.Status == apiextensionsv1.ConditionTrue {
					crdEstablished = true
				}
				if cond.Type == apiextensionsv1.NamesAccepted && cond.Status == apiextensionsv1.ConditionTrue {
					namesAccepted = true
				}
			}

			if !crdEstablished || !namesAccepted {
				return false, nil
			}
		}

		return true, nil
	}, 1*time.Minute)
	if err != nil {
		return errors.Wrap(err, "failed to wait for crd installation")
	}

	c.logger.Info("couchbase crds installed")

	return nil
}

func (c *Controller) GetSecret(ctx context.Context, namespace string, name string) (*corev1.Secret, error) {
	kubes, err := kubernetes.NewForConfig(c.restConfig)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create kubernetes client")
	}

	secret, err := kubes.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "failed to get secret")
	}

	return secret, nil
}

func (c *Controller) CreateSecret(ctx context.Context, namespace string, secret *corev1.Secret) error {
	kubes, err := kubernetes.NewForConfig(c.restConfig)
	if err != nil {
		return errors.Wrap(err, "failed to create kubernetes client")
	}

	// Check if the secret already exists, create or update
	_, err = kubes.CoreV1().Secrets(namespace).Get(ctx, secret.Name, metav1.GetOptions{})
	if err == nil {
		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			currentSecret, err := kubes.CoreV1().Secrets(namespace).Get(ctx, secret.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}

			currentSecret.Data = secret.Data
			_, updateErr := kubes.CoreV1().Secrets(namespace).Update(ctx, currentSecret, metav1.UpdateOptions{})
			return updateErr
		})
		if err != nil {
			return err
		}
	} else {
		_, err = kubes.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *Controller) InstallGhcrSecret(ctx context.Context, namespace string) error {
	c.logger.Info("installing ghcr secret", zap.String("namespace", namespace))

	// base64 encoding username and password..
	auth := c.ghcrUser + ":" + c.ghcrToken
	auth = base64.StdEncoding.EncodeToString([]byte(auth))

	data := `{"auths":{"` + "ghcr.io" + `":{"auth":"` + auth + `"}}}`

	return c.CreateSecret(ctx, namespace, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: GhcrSecretName,
		},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(data),
		},
		Type: corev1.SecretTypeDockerConfigJson,
	})
}

func (c *Controller) CreateBasicAuthSecret(
	ctx context.Context,
	namespace string,
	secretName,
	username string,
	password string,
) error {
	return c.CreateSecret(ctx, namespace, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: secretName,
		},
		Data: map[string][]byte{
			corev1.BasicAuthUsernameKey: []byte(username),
			corev1.BasicAuthPasswordKey: []byte(password),
		},
		Type: corev1.SecretTypeOpaque,
	})
}

func (c *Controller) ListNamespaces(ctx context.Context) (*corev1.NamespaceList, error) {
	kubes, err := kubernetes.NewForConfig(c.restConfig)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create kubernetes client")
	}

	namespaces, err := kubes.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "failed to list namespaces")
	}

	return namespaces, nil
}

func (c *Controller) CreateNamespace(ctx context.Context, namespace string, labels map[string]string) error {
	c.logger.Info("creating namespace", zap.String("namespace", namespace))

	kubes, err := kubernetes.NewForConfig(c.restConfig)
	if err != nil {
		return errors.Wrap(err, "failed to create kubernetes client")
	}

	_, err = kubes.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   namespace,
			Labels: labels,
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to create namespace")
	}

	return nil
}

func (c *Controller) DeleteNamespaces(ctx context.Context, namespaceNames []string) error {
	kubes, err := kubernetes.NewForConfig(c.restConfig)
	if err != nil {
		return errors.Wrap(err, "failed to create kubernetes client")
	}

	for _, namespace := range namespaceNames {
		c.logger.Info("deleting namespace", zap.String("namespace", namespace))

		err = kubes.CoreV1().Namespaces().Delete(ctx, namespace, metav1.DeleteOptions{})
		if err != nil {
			return errors.Wrap(err, "failed to delete namespace")
		}
	}

	c.logger.Info("waiting for namespaces to disappear")

	err = waitForFunc(ctx, func(ctx context.Context) (bool, error) {
		namespaces, err := kubes.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, errors.Wrap(err, "failed to get namespaces")
		}

		hasRemainingNamespace := false
		for _, ns := range namespaces.Items {
			if slices.Contains(namespaceNames, ns.Name) {
				hasRemainingNamespace = true
			}
		}

		if hasRemainingNamespace {
			return false, nil
		}

		return true, nil
	}, 1*time.Minute)
	if err != nil {
		return errors.Wrap(err, "failed to wait for namespace deletion")
	}

	c.logger.Info("namespaces deleted")

	return nil
}

func (c *Controller) FindAdmissionController(ctx context.Context) (string, error) {
	kubes, err := kubernetes.NewForConfig(c.restConfig)
	if err != nil {
		return "", errors.Wrap(err, "failed to create kubernetes client")
	}

	namespaces, err := kubes.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", errors.Wrap(err, "failed to list namespaces")
	}

	for _, namespace := range namespaces.Items {
		deployments, err := kubes.AppsV1().Deployments(namespace.Name).List(ctx, metav1.ListOptions{})
		if err != nil {
			return "", errors.Wrap(err, "failed to list deployments")
		}

		for _, deployment := range deployments.Items {
			if deployment.Name == DefaultAdmissionControllerName {
				return namespace.Name, nil
			}
		}
	}

	return "", nil
}

func (c *Controller) InstallGlobalAdmissionController(ctx context.Context, namespace string, version string) error {
	if namespace == "" {
		namespace = CbdcAdmissionControllerNamespace
	}

	imagePath := ""
	if version != "" {
		foundImagePath, err := GetAdmissionControllerImage(ctx, version)
		if err != nil {
			return errors.Wrap(err, "failed to identify image")
		}

		imagePath = foundImagePath
	}

	existingNamespace, err := c.FindAdmissionController(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to check for existing controller")
	}

	if existingNamespace != "" {
		c.logger.Info("found existing install, removing...")

		err := c.UninstallGlobalAdmissionController(ctx, existingNamespace)
		if err != nil {
			return errors.Wrap(err, "failed to uninstall existing controller")
		}
	}

	c.logger.Info("installing admission controller", zap.String("namespace", namespace))

	kubes, err := kubernetes.NewForConfig(c.restConfig)
	if err != nil {
		return errors.Wrap(err, "failed to create kubernetes client")
	}

	if namespace != "default" {
		err := c.CreateNamespace(ctx, namespace, map[string]string{
			"cbdc2.type": "admission",
		})
		if err != nil {
			return errors.Wrap(err, "failed to create admission controller namespace")
		}
	}

	c.logger.Info("installing admission controller ghcr secret", zap.String("namespace", namespace))

	err = c.InstallGhcrSecret(ctx, namespace)
	if err != nil {
		return errors.Wrap(err, "failed to install ghcr secret")
	}

	c.logger.Info("installing admission controller", zap.String("namespace", namespace))

	createArgs := []string{
		"create",
		"admission",
		"--namespace", namespace,
		"--replicas", "1",
		"--image-pull-secret", GhcrSecretName,
	}
	if imagePath != "" {
		createArgs = append(createArgs,
			"--image", imagePath)
	}

	err = c.caoExecAndPipe(ctx, c.logger, createArgs)
	if err != nil {
		return errors.Wrap(err, "failed to create admission controller")
	}

	c.logger.Info("waiting for admission controller to start")

	err = waitForFunc(ctx, func(ctx context.Context) (bool, error) {
		admDeployment, err := kubes.AppsV1().Deployments(namespace).Get(ctx, DefaultAdmissionControllerName, metav1.GetOptions{})
		if err != nil {
			return false, errors.Wrap(err, "failed to get admission controller info")
		}

		if admDeployment == nil {
			return false, errors.New("failed to find admission controller")
		}

		deploymentAvailable := false
		for _, cond := range admDeployment.Status.Conditions {
			if cond.Type == appsv1.DeploymentAvailable && cond.Status == corev1.ConditionTrue {
				deploymentAvailable = true
			}
		}

		if !deploymentAvailable {
			return false, nil
		}

		return true, nil
	}, 1*time.Minute)
	if err != nil {
		return errors.Wrap(err, "failed to wait for admission controller installation")
	}

	c.logger.Info("admission controller installed")
	return nil
}

func (c *Controller) UninstallGlobalAdmissionController(ctx context.Context, namespace string) error {
	if namespace == "" {
		namespace = CbdcAdmissionControllerNamespace
	}

	c.logger.Info("uninstalling admission controller", zap.String("namespace", namespace))

	err := c.caoExecAndPipe(ctx, c.logger, []string{
		"delete",
		"admission",
		"--namespace", namespace,
	})
	if err != nil {
		return errors.Wrap(err, "failed to delete admission controller")
	}

	kubes, err := kubernetes.NewForConfig(c.restConfig)
	if err != nil {
		return errors.Wrap(err, "failed to create kubernetes client")
	}

	c.logger.Info("waiting for admission controller to be deleted")

	err = waitForFunc(ctx, func(ctx context.Context) (bool, error) {
		deployments, err := kubes.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, errors.Wrap(err, "failed to get admission controller info")
		}

		for _, deployment := range deployments.Items {
			if deployment.Name == "DefaultAdmissionControllerName" {
				return false, nil
			}
		}

		return true, nil
	}, 1*time.Minute)
	if err != nil {
		return errors.Wrap(err, "failed to wait for admission controller deletion")
	}

	if namespace != "default" {
		err := c.DeleteNamespaces(ctx, []string{namespace})
		if err != nil {
			return errors.Wrap(err, "failed to delete admission controller namespace")
		}
	}

	c.logger.Info("admission controller uninstalled")
	return nil
}

func (c *Controller) InstallOperator(ctx context.Context, namespace string, version string, needRhcc bool) error {
	if namespace == "" {
		return errors.New("namespace must be specified")
	}

	imagePath := ""
	if version != "" {
		foundImagePath, err := GetOperatorImage(ctx, version, needRhcc)
		if err != nil {
			return errors.Wrap(err, "failed to identify image")
		}

		imagePath = foundImagePath
	}

	c.logger.Info("installing operator", zap.String("namespace", namespace))

	kubes, err := kubernetes.NewForConfig(c.restConfig)
	if err != nil {
		return errors.Wrap(err, "failed to create kubernetes client")
	}

	c.logger.Info("installing operator ghcr secret", zap.String("namespace", namespace))

	err = c.InstallGhcrSecret(ctx, namespace)
	if err != nil {
		return errors.Wrap(err, "failed to install ghcr secret")
	}

	c.logger.Info("installing operator", zap.String("namespace", namespace))

	createArgs := []string{
		"create",
		"operator",
		"--namespace", namespace,
		"--image-pull-secret", GhcrSecretName,
	}
	if imagePath != "" {
		createArgs = append(createArgs,
			"--image", imagePath)
	}

	err = c.caoExecAndPipe(ctx, c.logger, createArgs)
	if err != nil {
		return errors.Wrap(err, "failed to create operator")
	}

	c.logger.Info("waiting for operator to start")

	err = waitForFunc(ctx, func(ctx context.Context) (bool, error) {
		deployment, err := kubes.AppsV1().Deployments(namespace).Get(ctx, DefaultOperatorName, metav1.GetOptions{})
		if err != nil {
			return false, errors.Wrap(err, "failed to get admission controller info")
		}

		deploymentAvailable := false
		for _, cond := range deployment.Status.Conditions {
			if cond.Type == appsv1.DeploymentAvailable && cond.Status == corev1.ConditionTrue {
				deploymentAvailable = true
			}
		}

		if !deploymentAvailable {
			return false, nil
		}

		return true, nil
	}, 1*time.Minute)
	if err != nil {
		return errors.Wrap(err, "failed to wait for operation creation")
	}

	c.logger.Info("operator installed")
	return nil
}

func (c *Controller) createUnstructuredResource(ctx context.Context, namespace string, res *unstructured.Unstructured) error {
	c.logger.Debug("creating unstructured resource",
		zap.String("namespace", namespace),
		zap.Any("res", res))

	dyna, err := dynamic.NewForConfig(c.restConfig)
	if err != nil {
		return errors.Wrap(err, "failed to create dynamic client")
	}

	gvk := res.GetObjectKind().GroupVersionKind()
	gvr := schema.GroupVersionResource{
		Group:    gvk.Group,
		Version:  gvk.Version,
		Resource: strings.ToLower(gvk.Kind) + "s",
	}

	_, err = dyna.Resource(gvr).Namespace(namespace).Create(ctx, res, metav1.CreateOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to create resource")
	}

	c.logger.Debug("created unstructured resource")

	return nil
}

type CouchbaseClusterStatus struct {
	Conditions []appsv1.DeploymentCondition `json:"conditions,omitempty"`
}

func (c *Controller) ParseCouchbaseClusterStatus(
	res *unstructured.Unstructured,
) (*CouchbaseClusterStatus, error) {
	statusObj := res.Object["status"]

	statusBytes, err := json.Marshal(statusObj)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal to status object")
	}

	var out CouchbaseClusterStatus
	err = json.Unmarshal(statusBytes, &out)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal to status object")
	}

	return &out, nil
}

func (c *Controller) GetCouchbaseCluster(
	ctx context.Context,
	namespace string,
	name string,
) (*unstructured.Unstructured, error) {
	dyna, err := dynamic.NewForConfig(c.restConfig)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create dynamic client")
	}

	cluster, err := dyna.Resource(schema.GroupVersionResource{
		Group:    "couchbase.com",
		Version:  "v2",
		Resource: "couchbaseclusters",
	}).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "failed to get unstructured couchbase cluster resource")
	}

	return cluster, nil
}

func (c *Controller) CreateCouchbaseCluster(
	ctx context.Context,
	namespace string,
	name string,
	labels map[string]string,
	spec interface{},
) error {
	c.logger.Info("creating couchbase cluster", zap.String("namespace", namespace))

	err := c.createUnstructuredResource(ctx, namespace, &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "couchbase.com/v2",
			"kind":       "CouchbaseCluster",
			"metadata": map[string]interface{}{
				"name":   name,
				"labels": labels,
			},
			"spec": spec,
		},
	})
	if err != nil {
		return errors.Wrap(err, "failed to create cluster")
	}

	c.logger.Info("waiting for couchbase cluster to start")

	err = waitForFunc(ctx, func(ctx context.Context) (bool, error) {
		cluster, err := c.GetCouchbaseCluster(ctx, namespace, name)
		if err != nil {
			return false, errors.Wrap(err, "failed to get couchbase cluster info")
		}

		status, err := c.ParseCouchbaseClusterStatus(cluster)
		if err != nil {
			return false, errors.Wrap(err, "failed to parse status data")
		}

		clusterAvailable := false
		for _, cond := range status.Conditions {
			if cond.Type == "Available" && cond.Status == "True" {
				clusterAvailable = true
			}
		}

		if !clusterAvailable {
			return false, nil
		}

		return true, nil
	}, 10*time.Minute)
	if err != nil {
		return errors.Wrap(err, "failed to wait for couchbase cluster")
	}

	c.logger.Info("couchbase cluster created")

	return nil
}

func (c *Controller) CreateCbdcCngService(ctx context.Context, namespace string, clusterName string) error {
	c.logger.Info("creating dino cng service", zap.String("namespace", namespace))

	kubes, err := kubernetes.NewForConfig(c.restConfig)
	if err != nil {
		return errors.Wrap(err, "failed to create kubernetes client")
	}

	origSvcName := fmt.Sprintf("%s-cloud-native-gateway-service", clusterName)
	cngSvc, err := kubes.CoreV1().Services(namespace).Get(ctx, origSvcName, metav1.GetOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to find cng service")
	}

	svcName := fmt.Sprintf("cbdc2-%s-cng-service", clusterName)
	_, err = kubes.CoreV1().Services(namespace).Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: svcName,
		},
		Spec: corev1.ServiceSpec{
			Selector: cngSvc.Spec.Selector,
			Ports:    cngSvc.Spec.Ports,
			Type:     corev1.ServiceTypeNodePort,
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to create dino cng service")
	}

	// NodePort services get assigned ports at creation time.  No need to
	// wait for it to be "Available".

	c.logger.Info("created dino cng service")
	return nil
}

func (c *Controller) CreateRoute(ctx context.Context, namespace string, routeName string, spec interface{}) error {
	c.logger.Info("creating route", zap.String("namespace", namespace))

	err := c.createUnstructuredResource(ctx, namespace, &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "route.openshift.io/v1",
			"kind":       "Route",
			"metadata": map[string]interface{}{
				"name": routeName,
			},
			"spec": spec,
		},
	})
	if err != nil {
		return errors.Wrap(err, "failed to create route")
	}

	return nil
}

func (c *Controller) GetRouteHost(ctx context.Context, namespace string, name string) (string, error) {
	c.logger.Debug("getting route host",
		zap.String("namespace", namespace),
		zap.String("name", name))

	dyna, err := dynamic.NewForConfig(c.restConfig)
	if err != nil {
		return "", errors.Wrap(err, "failed to create dynamic client")
	}

	route, err := dyna.Resource(schema.GroupVersionResource{
		Group:    "route.openshift.io",
		Version:  "v1",
		Resource: "routes",
	}).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", errors.Wrap(err, "failed to get route")
	}

	spec, ok := route.Object["spec"].(map[string]interface{})
	if !ok {
		return "", errors.New("spec was not an object")
	}

	host, ok := spec["host"].(string)
	if !ok {
		return "", errors.New("host was not a string")
	}

	return host, nil
}

func (c *Controller) GetNodes(
	ctx context.Context,
) (*corev1.NodeList, error) {
	kubes, err := kubernetes.NewForConfig(c.restConfig)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create kubernetes client")
	}

	nodes, err := kubes.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "failed to get nodes")
	}

	return nodes, nil
}

func (c *Controller) GetService(
	ctx context.Context,
	namespace string,
	name string,
) (*corev1.Service, error) {
	kubes, err := kubernetes.NewForConfig(c.restConfig)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create kubernetes client")
	}

	service, err := kubes.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "failed to get service resource")
	}

	return service, nil
}
