package caocontrol

import (
	"context"
	"fmt"
	"strings"
	"time"

	routev1 "github.com/openshift/api/route/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned"
	routeclient "github.com/openshift/client-go/route/clientset/versioned"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
)

// getK8sRestConfig returns a Kubernetes client configuration based on the provided kubeconfig path or default paths.
func getK8sRestConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig == "" {
		// If no kubeconfig path is provided, try using the default paths
		config, err := clientcmd.LoadFromFile(clientcmd.RecommendedHomeFile)
		if err == nil {
			return clientcmd.NewDefaultClientConfig(*config, &clientcmd.ConfigOverrides{}).ClientConfig()
		}

		config, err = clientcmd.LoadFromFile(clientcmd.RecommendedConfigDir)
		if err == nil {
			return clientcmd.NewDefaultClientConfig(*config, &clientcmd.ConfigOverrides{}).ClientConfig()
		}
	} else {
		// If a kubeconfig path is provided, use it
		config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err == nil {
			return config, nil
		}
	}

	return nil, fmt.Errorf("unable to load kubeconfig")
}

// getImageVersionFromDeployment extracts the image version from the Pod template specification of a Deployment.
func getImageVersionFromDeployment(deployment *appsv1.Deployment) (string, error) {
	containers := deployment.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		return "", fmt.Errorf("no containers found in the Pod template")
	}

	// Assuming that the Deployment has only one container, you may modify the logic accordingly
	image := containers[0].Image
	imageParts := strings.Split(image, ":")
	if len(imageParts) < 2 {
		return "", fmt.Errorf("unable to determine image version from: %s", image)
	}

	return imageParts[1], nil
}

func createK8sSecret(clientset *kubernetes.Clientset, secret *corev1.Secret, namespace string) error {
	ctx := context.TODO()
	// Check if the secret already exists, create or update
	_, err := clientset.CoreV1().Secrets(namespace).Get(ctx, secret.Name, metav1.GetOptions{})
	if err == nil {
		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			currentSecret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, secret.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}

			currentSecret.Data = secret.Data
			_, updateErr := clientset.CoreV1().Secrets(namespace).Update(ctx, currentSecret, metav1.UpdateOptions{})
			return updateErr
		})
		if err != nil {
			return err
		}
	} else {
		_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
		if err != nil {
			return err
		}
	}

	return nil
}

func WaitForConditionFuncMet(conditionFunc func(context.Context) (bool, error), timeout time.Duration) error {
	return wait.PollUntilContextTimeout(context.Background(), time.Second, timeout, true, conditionFunc)
}

func IsClusterOpenshift(clientset *kubernetes.Clientset) bool {
	_, err := clientset.Discovery().ServerResourcesForGroupVersion("route.openshift.io/v1")
	return err == nil
}

func getHostDomain(config *rest.Config) (string, error) {
	// Create an OpenShift Config clientset
	configClient, err := configclient.NewForConfig(config)
	if err != nil {
		return "", fmt.Errorf("Error creating OpenShift Config clientset: %v\n", err)
	}

	ingressConfig, err := configClient.ConfigV1().Ingresses().Get(context.TODO(), "cluster", metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("Error getting IngressConfig: %v\n", err)
	}

	return ingressConfig.Spec.Domain, nil
}

func createOcRoute(config *rest.Config, route *routev1.Route, namespace string) error {
	rc, err := getOcRouteClient(config)
	if err != nil {
		return fmt.Errorf("failed to get oc route client %v\n", err)
	}

	ctx := context.TODO()
	// delete, if exists
	_, err = rc.RouteV1().Routes(namespace).Get(ctx, route.Name, metav1.GetOptions{})
	if err == nil {
		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			delErr := rc.RouteV1().Routes(namespace).Delete(ctx, route.Name, metav1.DeleteOptions{})
			return delErr
		})
		if err != nil {
			return err
		}
	}
	_, err = rc.RouteV1().Routes(namespace).Create(ctx, route, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create OpenShift Route %v\n", err)
	}

	return nil
}

func getOcRouteClient(config *rest.Config) (*routeclient.Clientset, error) {
	return routeclient.NewForConfig(config)
}

func getOcRoutes(config *rest.Config, namespace string) (*routev1.RouteList, error) {
	rc, err := getOcRouteClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to get oc route client %v\n", err)
	}

	return rc.RouteV1().Routes(namespace).List(context.Background(), metav1.ListOptions{})
}
