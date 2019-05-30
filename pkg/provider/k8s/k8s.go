package k8s

import (
	"context"
	"fmt"
	"log"

	"github.com/pkg/errors"
	"gopkg.in/alecthomas/kingpin.v2"
	appsV1 "k8s.io/api/apps/v1"
	apiCoreV1 "k8s.io/api/core/v1"
	apiExtensionsV1beta1 "k8s.io/api/extensions/v1beta1"
	rbac "k8s.io/api/rbac/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	apiMetaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"

	"strings"

	"github.com/prometheus/prombench/pkg/provider"

	apiServerExtensionsV1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiServerExtensionsClient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func init() {
	apiServerExtensionsV1beta1.AddToScheme(scheme.Scheme)
}

// Resource holds the resource objects after parsing deployment files.
type Resource struct {
	FileName string
	Objects  []runtime.Object
}

// K8s holds the fields used to generate API reqeust from within a cluster.
type K8s struct {
	clt          *kubernetes.Clientset
	ApiExtClient *apiServerExtensionsClient.Clientset
	// DeploymentFiles files provided from the cli.
	DeploymentFiles []string
	// Vaiables to subtitude in the DeploymentFiles.
	// These are also used when the command requires some variables that are not provided by the deployment file.
	DeploymentVars map[string]string
	// K8s resource.runtime objects after parsing the template variables, grouped by filename.
	resources []Resource

	ctx context.Context
}

// New returns a k8s client that can apply and delete resources.
func New(ctx context.Context, config *clientcmdapi.Config) (*K8s, error) {
	var restConfig *rest.Config
	var err error
	if config == nil {
		restConfig, err = rest.InClusterConfig()
	} else {
		restConfig, err = clientcmd.NewDefaultClientConfig(*config, &clientcmd.ConfigOverrides{}).ClientConfig()
	}
	if err != nil {
		return nil, errors.Wrapf(err, "k8s config error")
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, errors.Wrapf(err, "k8s client error")
	}

	apiExtClientset, err := apiServerExtensionsClient.NewForConfig(restConfig)
	if err != nil {
		return nil, errors.Wrapf(err, "k8s api extensions client error")
	}

	return &K8s{
		ctx:            ctx,
		clt:            clientset,
		ApiExtClient:   apiExtClientset,
		DeploymentVars: make(map[string]string),
	}, nil
}

// GetResourses is a getter function for Resources field in K8s.
func (c *K8s) GetResourses() []Resource {
	return c.resources
}

// DeploymentsParse parses the k8s objects deployment files and saves the result as k8s objects grouped by the filename.
// Any variables passed to the cli will be replaced in the resources files following the golang text template format.
func (c *K8s) DeploymentsParse(*kingpin.ParseContext) error {
	deploymentResource, err := provider.DeploymentsParse(c.DeploymentFiles, c.DeploymentVars)
	if err != nil {
		log.Fatalf("Couldn't parse deployment files: %v", err)
	}

	for _, deployment := range deploymentResource {

		decode := scheme.Codecs.UniversalDeserializer().Decode
		k8sObjects := make([]runtime.Object, 0)

		for _, text := range strings.Split(string(deployment.Content), provider.Separator) {
			text = strings.TrimSpace(text)
			if len(text) == 0 {
				continue
			}

			resource, _, err := decode([]byte(text), nil, nil)
			if err != nil {
				return errors.Wrapf(err, "decoding the resource file:%v, section:%v...", deployment.FileName, text[:100])
			}
			if resource == nil {
				continue
			}
			k8sObjects = append(k8sObjects, resource)
		}
		if len(k8sObjects) > 0 {
			c.resources = append(c.resources, Resource{FileName: deployment.FileName, Objects: k8sObjects})
		}
	}
	return nil
}

// ResourceApply applies k8s objects.
// The input is a slice of structs containing the filename and the slice of k8s objects present in the file.
func (c *K8s) ResourceApply(deployments []Resource) error {

	var err error
	for _, deployment := range deployments {
		for _, resource := range deployment.Objects {
			switch kind := strings.ToLower(resource.GetObjectKind().GroupVersionKind().Kind); kind {
			case "clusterrole":
				err = c.clusterRoleApply(resource)
			case "clusterrolebinding":
				err = c.clusterRoleBindingApply(resource)
			case "configmap":
				err = c.configMapApply(resource)
			case "daemonset":
				err = c.daemonSetApply(resource)
			case "deployment":
				err = c.deploymentApply(resource)
			case "ingress":
				err = c.ingressApply(resource)
			case "namespace":
				err = c.nameSpaceApply(resource)
			case "role":
				err = c.roleApply(resource)
			case "rolebinding":
				err = c.roleBindingApply(resource)
			case "service":
				err = c.serviceApply(resource)
			case "serviceaccount":
				err = c.serviceAccountApply(resource)
			case "secret":
				err = c.secretApply(resource)
			case "persistentvolumeclaim":
				err = c.persistentVolumeClaimApply(resource)
			case "customresourcedefinition":
				err = c.customResourceApply(resource)
			default:
				err = fmt.Errorf("creating request for unimplimented resource type:%v", kind)
			}
			if err != nil {
				log.Printf("error applying '%v' err:%v \n", deployment.FileName, err)
			}
		}
	}
	return nil
}

// ResourceDelete deletes k8s objects.
// The input is a slice of structs containing the filename and the slice of k8s objects present in the file.
func (c *K8s) ResourceDelete(deployments []Resource) error {

	var err error
	for _, deployment := range deployments {
		for _, resource := range deployment.Objects {
			switch kind := strings.ToLower(resource.GetObjectKind().GroupVersionKind().Kind); kind {
			case "clusterrole":
				err = c.clusterRoleDelete(resource)
			case "clusterrolebinding":
				err = c.clusterRoleBindingDelete(resource)
			case "configmap":
				err = c.configMapDelete(resource)
			case "daemonset":
				err = c.daemonsetDelete(resource)
			case "deployment":
				err = c.deploymentDelete(resource)
			case "ingress":
				err = c.ingressDelete(resource)
			case "namespace":
				err = c.namespaceDelete(resource)
			case "role":
				err = c.roleDelete(resource)
			case "rolebinding":
				err = c.roleBindingDelete(resource)
			case "service":
				err = c.serviceDelete(resource)
			case "serviceaccount":
				err = c.serviceAccountDelete(resource)
			case "secret":
				err = c.secretDelete(resource)
			case "persistentvolumeclaim":
				err = c.persistentVolumeClaimDelete(resource)
			case "customresourcedefinition":
				err = c.customResourceDelete(resource)
			default:
				err = fmt.Errorf("deleting request for unimplimented resource type:%v", kind)
			}
			if err != nil {
				log.Printf("error deleting '%v' err:%v \n", deployment.FileName, err)
			}
		}
	}
	return nil
}

// Functions to create different K8s objects.
func (c *K8s) clusterRoleApply(resource runtime.Object) error {
	req := resource.(*rbac.ClusterRole)
	kind := resource.GetObjectKind().GroupVersionKind().Kind

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":
		client := c.clt.RbacV1().ClusterRoles()

		list, err := client.List(apiMetaV1.ListOptions{})
		if err != nil {
			return errors.Wrapf(err, "listing resource : %v", kind)
		}

		var exists bool
		for _, l := range list.Items {
			if l.Name == req.Name {
				exists = true
				break
			}
		}

		if exists {
			if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				_, err := client.Update(req)
				return err
			}); err != nil {
				return errors.Wrapf(err, "resource update failed - kind: %v, name: %v", kind, req.Name)
			}
			log.Printf("resource updated - kind: %v, name: %v", kind, req.Name)
			return nil
		} else if _, err := client.Create(req); err != nil {
			return errors.Wrapf(err, "resource creation failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource created - kind: %v, name: %v", kind, req.Name)
		return nil
	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}

}

func (c *K8s) clusterRoleBindingApply(resource runtime.Object) error {
	req := resource.(*rbac.ClusterRoleBinding)
	kind := resource.GetObjectKind().GroupVersionKind().Kind

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":
		client := c.clt.RbacV1().ClusterRoleBindings()
		list, err := client.List(apiMetaV1.ListOptions{})
		if err != nil {
			return errors.Wrapf(err, "error listing resource : %v, name: %v", kind, req.Name)
		}

		var exists bool
		for _, l := range list.Items {
			if l.Name == req.Name {
				exists = true
				break
			}
		}

		if exists {
			if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				_, err := client.Update(req)
				return err
			}); err != nil {
				return errors.Wrapf(err, "resource update failed - kind: %v, name: %v", kind, req.Name)
			}
			log.Printf("resource updated - kind: %v, name: %v", kind, req.Name)
			return nil
		} else if _, err := client.Create(req); err != nil {
			return errors.Wrapf(err, "resource creation failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource created - kind: %v, name: %v", kind, req.Name)
	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
	return nil
}

func (c *K8s) configMapApply(resource runtime.Object) error {
	req := resource.(*apiCoreV1.ConfigMap)
	kind := resource.GetObjectKind().GroupVersionKind().Kind
	if len(req.Namespace) == 0 {
		req.Namespace = "default"
	}

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":

		client := c.clt.CoreV1().ConfigMaps(req.Namespace)

		list, err := client.List(apiMetaV1.ListOptions{})
		if err != nil {
			return errors.Wrapf(err, "error listing resource : %v, name: %v", kind, req.Name)
		}

		var exists bool
		for _, l := range list.Items {
			if l.Name == req.Name {
				exists = true
				break
			}
		}

		if exists {
			if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				_, err := client.Update(req)
				return err
			}); err != nil {
				return errors.Wrapf(err, "resource update failed - kind: %v, name: %v", kind, req.Name)
			}
			log.Printf("resource updated - kind: %v, name: %v", kind, req.Name)
			return nil
		} else if _, err := client.Create(req); err != nil {
			return errors.Wrapf(err, "resource creation failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource created - kind: %v, name: %v", kind, req.Name)
	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
	return nil
}

func (c *K8s) daemonSetApply(resource runtime.Object) error {
	req := resource.(*appsV1.DaemonSet)
	kind := resource.GetObjectKind().GroupVersionKind().Kind
	if len(req.Namespace) == 0 {
		req.Namespace = "default"
	}

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":
		client := c.clt.AppsV1().DaemonSets(req.Namespace)
		list, err := client.List(apiMetaV1.ListOptions{})
		if err != nil {
			return errors.Wrapf(err, "error listing resource : %v, name: %v", kind, req.Name)
		}

		var exists bool
		for _, l := range list.Items {
			if l.Name == req.Name {
				exists = true
				break
			}
		}

		if exists {
			if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				_, err := client.Update(req)
				return err
			}); err != nil {
				return errors.Wrapf(err, "resource update failed - kind: %v, name: %v", kind, req.Name)
			}
			log.Printf("resource updated - kind: %v, name: %v", kind, req.Name)
			return nil
		} else if _, err := client.Create(req); err != nil {
			return errors.Wrapf(err, "resource creation failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource created - kind: %v, name: %v", kind, req.Name)
	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
	c.daemonsetReady(resource)
	return nil
}

func (c *K8s) deploymentApply(resource runtime.Object) error {
	req := resource.(*appsV1.Deployment)
	kind := resource.GetObjectKind().GroupVersionKind().Kind
	if len(req.Namespace) == 0 {
		req.Namespace = "default"
	}

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":
		client := c.clt.AppsV1().Deployments(req.Namespace)
		list, err := client.List(apiMetaV1.ListOptions{})
		if err != nil {
			return errors.Wrapf(err, "error listing resource : %v, name: %v", kind, req.Name)
		}

		var exists bool
		for _, l := range list.Items {
			if l.Name == req.Name {
				exists = true
				break
			}
		}

		if exists {
			if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				_, err := client.Update(req)
				return err
			}); err != nil {
				return errors.Wrapf(err, "resource update failed - kind: %v, name: %v", kind, req.Name)
			}
			log.Printf("resource updated - kind: %v, name: %v", kind, req.Name)
			return nil
		} else if _, err := client.Create(req); err != nil {
			return errors.Wrapf(err, "resource creation failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource created - kind: %v, name: %v", kind, req.Name)
	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
	return provider.RetryUntilTrue(
		fmt.Sprintf("applying deployment:%v", req.Name),
		provider.GlobalRetryCount,
		func() (bool, error) { return c.deploymentReady(resource) })
}

func (c *K8s) customResourceApply(resource runtime.Object) error {
	req := resource.(*apiServerExtensionsV1beta1.CustomResourceDefinition)
	kind := resource.GetObjectKind().GroupVersionKind().Kind
	if len(req.Namespace) == 0 {
		req.Namespace = "default"
	}

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1beta1":
		client := c.ApiExtClient.ApiextensionsV1beta1().CustomResourceDefinitions()
		list, err := client.List(apiMetaV1.ListOptions{})
		if err != nil {
			return errors.Wrapf(err, "error listing resource : %v, name: %v", kind, req.Name)
		}
		var exists bool
		for _, l := range list.Items {
			if l.Name == req.Name {
				exists = true
				break
			}
		}
		if exists {
			if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				_, err := client.Update(req)
				return err
			}); err != nil {
				return errors.Wrapf(err, "resource update failed - kind: %v, name: %v", kind, req.Name)
			}
			log.Printf("resource updated - kind: %v, name: %v", kind, req.Name)
			return nil
		} else if _, err := client.Create(req); err != nil {
			return errors.Wrapf(err, "resource creation failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource created - kind: %v, name: %v", kind, req.Name)
	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}

	return nil
}

func (c *K8s) ingressApply(resource runtime.Object) error {
	req := resource.(*apiExtensionsV1beta1.Ingress)
	kind := resource.GetObjectKind().GroupVersionKind().Kind
	if len(req.Namespace) == 0 {
		req.Namespace = "default"
	}

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1beta1":
		client := c.clt.ExtensionsV1beta1().Ingresses(req.Namespace)
		list, err := client.List(apiMetaV1.ListOptions{})
		if err != nil {
			return errors.Wrapf(err, "error listing resource : %v, name: %v", kind, req.Name)
		}

		var exists bool
		for _, l := range list.Items {
			if l.Name == req.Name {
				exists = true
				break
			}
		}

		if exists {
			if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				_, err := client.Update(req)
				return err
			}); err != nil {
				return errors.Wrapf(err, "resource update failed - kind: %v, name: %v", kind, req.Name)
			}
			log.Printf("resource updated - kind: %v, name: %v", kind, req.Name)
			return nil
		} else if _, err := client.Create(req); err != nil {
			return errors.Wrapf(err, "resource creation failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource created - kind: %v, name: %v", kind, req.Name)
	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
	return nil
}

func (c *K8s) nameSpaceApply(resource runtime.Object) error {
	req := resource.(*apiCoreV1.Namespace)
	kind := resource.GetObjectKind().GroupVersionKind().Kind

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":
		client := c.clt.CoreV1().Namespaces()
		list, err := client.List(apiMetaV1.ListOptions{})
		if err != nil {
			return errors.Wrapf(err, "error listing resource : %v, name: %v", kind, req.Name)
		}

		var exists bool
		for _, l := range list.Items {
			if l.Name == req.Name {
				exists = true
				break
			}
		}

		if exists {
			if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				_, err := client.Update(req)
				return err
			}); err != nil {
				return errors.Wrapf(err, "resource update failed - kind: %v, name: %v", kind, req.Name)
			}
			log.Printf("resource updated - kind: %v, name: %v", kind, req.Name)
			return nil
		} else if _, err := client.Create(req); err != nil {
			return errors.Wrapf(err, "resource creation failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource created - kind: %v, name: %v", kind, req.Name)

	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
	return nil
}

func (c *K8s) roleApply(resource runtime.Object) error {
	req := resource.(*rbac.Role)
	kind := resource.GetObjectKind().GroupVersionKind().Kind
	if len(req.Namespace) == 0 {
		req.Namespace = "default"
	}

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":
		client := c.clt.RbacV1().Roles(req.Namespace)
		list, err := client.List(apiMetaV1.ListOptions{})
		if err != nil {
			return errors.Wrapf(err, "error listing resource : %v, name: %v", kind, req.Name)
		}

		var exists bool
		for _, l := range list.Items {
			if l.Name == req.Name {
				exists = true
				break
			}
		}

		if exists {
			if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				_, err := client.Update(req)
				return err
			}); err != nil {
				return errors.Wrapf(err, "resource update failed - kind: %v, name: %v", kind, req.Name)
			}
			log.Printf("resource updated - kind: %v, name: %v", kind, req.Name)
			return nil
		} else if _, err := client.Create(req); err != nil {
			return errors.Wrapf(err, "resource creation failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource created - kind: %v, name: %v", kind, req.Name)
	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
	return nil
}

func (c *K8s) roleBindingApply(resource runtime.Object) error {
	req := resource.(*rbac.RoleBinding)
	kind := resource.GetObjectKind().GroupVersionKind().Kind
	if len(req.Namespace) == 0 {
		req.Namespace = "default"
	}

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":
		client := c.clt.RbacV1().RoleBindings(req.Namespace)
		list, err := client.List(apiMetaV1.ListOptions{})
		if err != nil {
			return errors.Wrapf(err, "error listing resource : %v, name: %v", kind, req.Name)
		}

		var exists bool
		for _, l := range list.Items {
			if l.Name == req.Name {
				exists = true
				break
			}
		}

		if exists {
			if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				_, err := client.Update(req)
				return err
			}); err != nil {
				return errors.Wrapf(err, "resource update failed - kind: %v, name: %v", kind, req.Name)
			}
			log.Printf("resource updated - kind: %v, name: %v", kind, req.Name)
			return nil
		} else if _, err := client.Create(req); err != nil {
			return errors.Wrapf(err, "resource creation failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource created - kind: %v, name: %v", kind, req.Name)
	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
	return nil
}

func (c *K8s) serviceAccountApply(resource runtime.Object) error {
	req := resource.(*apiCoreV1.ServiceAccount)
	kind := resource.GetObjectKind().GroupVersionKind().Kind
	if len(req.Namespace) == 0 {
		req.Namespace = "default"
	}

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":
		client := c.clt.CoreV1().ServiceAccounts(req.Namespace)
		list, err := client.List(apiMetaV1.ListOptions{})
		if err != nil {
			return errors.Wrapf(err, "error listing resource : %v, name: %v", kind, req.Name)
		}

		var exists bool
		for _, l := range list.Items {
			if l.Name == req.Name {
				exists = true
				break
			}
		}

		if exists {
			if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				_, err := client.Update(req)
				return err
			}); err != nil {
				return errors.Wrapf(err, "resource update failed - kind: %v, name: %v", kind, req.Name)
			}
			log.Printf("resource updated - kind: %v, name: %v", kind, req.Name)
			return nil
		} else if _, err := client.Create(req); err != nil {
			return errors.Wrapf(err, "resource creation failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource created - kind: %v, name: %v", kind, req.Name)
	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
	return nil
}

func (c *K8s) serviceApply(resource runtime.Object) error {
	req := resource.(*apiCoreV1.Service)
	kind := resource.GetObjectKind().GroupVersionKind().Kind
	if len(req.Namespace) == 0 {
		req.Namespace = "default"
	}

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":
		client := c.clt.CoreV1().Services(req.Namespace)
		list, err := client.List(apiMetaV1.ListOptions{})
		if err != nil {
			return errors.Wrapf(err, "error listing resource : %v, name: %v", kind, req.Name)
		}

		var exists bool
		for _, l := range list.Items {
			if l.Name == req.Name {
				exists = true
				break
			}
		}

		if exists {
			if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				_, err := client.Update(req)
				return err
			}); err != nil {
				return errors.Wrapf(err, "resource update failed - kind: %v, name: %v", kind, req.Name)
			}
			log.Printf("resource updated - kind: %v, name: %v", kind, req.Name)
			return nil
		} else if _, err := client.Create(req); err != nil {
			return errors.Wrapf(err, "resource creation failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource created - kind: %v, name: %v", kind, req.Name)
	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}

	return provider.RetryUntilTrue(
		fmt.Sprintf("applying service:%v", req.Name),
		provider.GlobalRetryCount,
		func() (bool, error) { return c.serviceExists(resource) })
}

func (c *K8s) secretApply(resource runtime.Object) error {
	req := resource.(*apiCoreV1.Secret)
	kind := req.GetObjectKind().GroupVersionKind().Kind
	if len(req.Namespace) == 0 {
		req.Namespace = "default"
	}
	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":
		client := c.clt.CoreV1().Secrets(req.Namespace)
		list, err := client.List(apiMetaV1.ListOptions{})
		if err != nil {
			return errors.Wrapf(err, "error listing resource : %v, name: %v", kind, req.Name)
		}

		var exists bool
		for _, l := range list.Items {
			if l.Name == req.Name {
				exists = true
				break
			}
		}

		if exists {
			if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				_, err := client.Update(req)
				return err
			}); err != nil {
				return errors.Wrapf(err, "resource update failed - kind: %v, name: %v", kind, req.Name)
			}
			log.Printf("resource updated - kind: %v, name: %v", kind, req.Name)
			return nil
		} else if _, err := client.Create(req); err != nil {
			return errors.Wrapf(err, "resource creation failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource created - kind: %v, name: %v", kind, req.Name)
	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
	return nil
}

func (c *K8s) persistentVolumeClaimApply(resource runtime.Object) error {
	req := resource.(*apiCoreV1.PersistentVolumeClaim)
	kind := req.GetObjectKind().GroupVersionKind().Kind
	if len(req.Namespace) == 0 {
		req.Namespace = "default"
	}
	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":
		client := c.clt.CoreV1().PersistentVolumeClaims(req.Namespace)
		list, err := client.List(apiMetaV1.ListOptions{})
		if err != nil {
			return errors.Wrapf(err, "error listing resource : %v, name: %v", kind, req.Name)
		}

		var exists bool
		for _, l := range list.Items {
			if l.Name == req.Name {
				exists = true
				break
			}
		}

		if exists {
			if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				_, err := client.Update(req)
				return err
			}); err != nil {
				return errors.Wrapf(err, "resource update failed - kind: %v, name: %v", kind, req.Name)
			}
			log.Printf("resource updated - kind: %v, name: %v", kind, req.Name)
			return nil
		} else if _, err := client.Create(req); err != nil {
			return errors.Wrapf(err, "resource creation failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource created - kind: %v, name: %v", kind, req.Name)
	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
	return nil
}

// Functions to delete different K8s objects.
func (c *K8s) clusterRoleDelete(resource runtime.Object) error {
	req := resource.(*rbac.ClusterRole)
	kind := resource.GetObjectKind().GroupVersionKind().Kind

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":
		client := c.clt.RbacV1().ClusterRoles()
		delPolicy := apiMetaV1.DeletePropagationForeground
		if err := client.Delete(req.Name, &apiMetaV1.DeleteOptions{PropagationPolicy: &delPolicy}); err != nil {
			return errors.Wrapf(err, "resource delete failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource deleted - kind: %v , name: %v", kind, req.Name)
	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
	return nil
}

func (c *K8s) clusterRoleBindingDelete(resource runtime.Object) error {
	req := resource.(*rbac.ClusterRoleBinding)
	kind := resource.GetObjectKind().GroupVersionKind().Kind

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":
		client := c.clt.RbacV1().ClusterRoleBindings()
		delPolicy := apiMetaV1.DeletePropagationForeground
		if err := client.Delete(req.Name, &apiMetaV1.DeleteOptions{PropagationPolicy: &delPolicy}); err != nil {
			return errors.Wrapf(err, "resource delete failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource deleted - kind: %v , name: %v", kind, req.Name)
	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
	return nil
}
func (c *K8s) configMapDelete(resource runtime.Object) error {
	req := resource.(*apiCoreV1.ConfigMap)
	kind := resource.GetObjectKind().GroupVersionKind().Kind
	if len(req.Namespace) == 0 {
		req.Namespace = "default"
	}

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":
		client := c.clt.CoreV1().ConfigMaps(req.Namespace)
		delPolicy := apiMetaV1.DeletePropagationForeground
		if err := client.Delete(req.Name, &apiMetaV1.DeleteOptions{PropagationPolicy: &delPolicy}); err != nil {
			return errors.Wrapf(err, "resource delete failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource deleted - kind: %v , name: %v", kind, req.Name)
	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
	return nil
}

func (c *K8s) daemonsetDelete(resource runtime.Object) error {
	req := resource.(*appsV1.DaemonSet)
	kind := resource.GetObjectKind().GroupVersionKind().Kind
	if len(req.Namespace) == 0 {
		req.Namespace = "default"
	}

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":
		client := c.clt.AppsV1().DaemonSets(req.Namespace)
		delPolicy := apiMetaV1.DeletePropagationForeground
		if err := client.Delete(req.Name, &apiMetaV1.DeleteOptions{PropagationPolicy: &delPolicy}); err != nil {
			return errors.Wrapf(err, "resource delete failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource deleted - kind: %v , name: %v", kind, req.Name)
	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
	return nil
}

func (c *K8s) deploymentDelete(resource runtime.Object) error {
	req := resource.(*appsV1.Deployment)
	kind := resource.GetObjectKind().GroupVersionKind().Kind
	if len(req.Namespace) == 0 {
		req.Namespace = "default"
	}

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":
		client := c.clt.AppsV1().Deployments(req.Namespace)
		delPolicy := apiMetaV1.DeletePropagationForeground
		if err := client.Delete(req.Name, &apiMetaV1.DeleteOptions{PropagationPolicy: &delPolicy}); err != nil {
			return errors.Wrapf(err, "resource delete failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource deleted - kind: %v , name: %v", kind, req.Name)
	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
	return nil
}

func (c *K8s) customResourceDelete(resource runtime.Object) error {
	req := resource.(*apiServerExtensionsV1beta1.CustomResourceDefinition)
	kind := resource.GetObjectKind().GroupVersionKind().Kind
	if len(req.Namespace) == 0 {
		req.Namespace = "default"
	}

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1beta1":
		client := c.ApiExtClient.ApiextensionsV1beta1().CustomResourceDefinitions()
		delPolicy := apiMetaV1.DeletePropagationForeground
		if err := client.Delete(req.Name, &apiMetaV1.DeleteOptions{PropagationPolicy: &delPolicy}); err != nil {
			return errors.Wrapf(err, "resource delete failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource deleted - kind: %v , name: %v", kind, req.Name)
	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}

	return nil
}

func (c *K8s) ingressDelete(resource runtime.Object) error {
	req := resource.(*apiExtensionsV1beta1.Ingress)
	kind := resource.GetObjectKind().GroupVersionKind().Kind
	if len(req.Namespace) == 0 {
		req.Namespace = "default"
	}

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1beta1":
		client := c.clt.ExtensionsV1beta1().Ingresses(req.Namespace)
		delPolicy := apiMetaV1.DeletePropagationForeground
		if err := client.Delete(req.Name, &apiMetaV1.DeleteOptions{PropagationPolicy: &delPolicy}); err != nil {
			return errors.Wrapf(err, "resource delete failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource deleted - kind: %v , name: %v", kind, req.Name)
	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
	return nil
}

func (c *K8s) namespaceDelete(resource runtime.Object) error {
	req := resource.(*apiCoreV1.Namespace)
	kind := resource.GetObjectKind().GroupVersionKind().Kind

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":
		client := c.clt.CoreV1().Namespaces()
		delPolicy := apiMetaV1.DeletePropagationForeground
		if err := client.Delete(req.Name, &apiMetaV1.DeleteOptions{PropagationPolicy: &delPolicy}); err != nil {
			return errors.Wrapf(err, "resource delete failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource deleting - kind: %v , name: %v", kind, req.Name)
		return provider.RetryUntilTrue(
			fmt.Sprintf("deleting namespace:%v", req.Name),
			2*provider.GlobalRetryCount,
			func() (bool, error) { return c.namespaceDeleted(resource) })
	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
}

func (c *K8s) roleDelete(resource runtime.Object) error {
	req := resource.(*rbac.Role)
	kind := resource.GetObjectKind().GroupVersionKind().Kind
	if len(req.Namespace) == 0 {
		req.Namespace = "default"
	}

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":
		client := c.clt.RbacV1().Roles(req.Namespace)
		delPolicy := apiMetaV1.DeletePropagationForeground
		if err := client.Delete(req.Name, &apiMetaV1.DeleteOptions{PropagationPolicy: &delPolicy}); err != nil {
			return errors.Wrapf(err, "resource delete failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource deleted - kind: %v , name: %v", kind, req.Name)
	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
	return nil
}

func (c *K8s) roleBindingDelete(resource runtime.Object) error {
	req := resource.(*rbac.RoleBinding)
	kind := resource.GetObjectKind().GroupVersionKind().Kind
	if len(req.Namespace) == 0 {
		req.Namespace = "default"
	}

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":
		client := c.clt.RbacV1().RoleBindings(req.Namespace)
		delPolicy := apiMetaV1.DeletePropagationForeground
		if err := client.Delete(req.Name, &apiMetaV1.DeleteOptions{PropagationPolicy: &delPolicy}); err != nil {
			return errors.Wrapf(err, "resource delete failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource deleted - kind: %v , name: %v", kind, req.Name)
	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
	return nil
}

func (c *K8s) serviceDelete(resource runtime.Object) error {
	req := resource.(*apiCoreV1.Service)
	kind := resource.GetObjectKind().GroupVersionKind().Kind
	if len(req.Namespace) == 0 {
		req.Namespace = "default"
	}

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":
		client := c.clt.CoreV1().Services(req.Namespace)
		delPolicy := apiMetaV1.DeletePropagationForeground
		if err := client.Delete(req.Name, &apiMetaV1.DeleteOptions{PropagationPolicy: &delPolicy}); err != nil {
			return errors.Wrapf(err, "resource delete failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource deleted - kind: %v , name: %v", kind, req.Name)
	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
	return nil
}

func (c *K8s) serviceAccountDelete(resource runtime.Object) error {
	req := resource.(*apiCoreV1.ServiceAccount)
	kind := resource.GetObjectKind().GroupVersionKind().Kind
	if len(req.Namespace) == 0 {
		req.Namespace = "default"
	}

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":
		client := c.clt.CoreV1().ServiceAccounts(req.Namespace)
		delPolicy := apiMetaV1.DeletePropagationForeground
		if err := client.Delete(req.Name, &apiMetaV1.DeleteOptions{PropagationPolicy: &delPolicy}); err != nil {
			return errors.Wrapf(err, "resource delete failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource deleted - kind: %v , name: %v", kind, req.Name)
	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
	return nil
}

func (c *K8s) secretDelete(resource runtime.Object) error {
	req := resource.(*apiCoreV1.Secret)
	kind := resource.GetObjectKind().GroupVersionKind().Kind
	if len(req.Namespace) == 0 {
		req.Namespace = "default"
	}
	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":
		client := c.clt.CoreV1().Secrets(req.Namespace)
		delPolicy := apiMetaV1.DeletePropagationForeground
		if err := client.Delete(req.Name, &apiMetaV1.DeleteOptions{PropagationPolicy: &delPolicy}); err != nil {
			return errors.Wrapf(err, "resource delete failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource deleted - kind: %v , name: %v", kind, req.Name)
	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
	return nil
}

func (c *K8s) persistentVolumeClaimDelete(resource runtime.Object) error {
	req := resource.(*apiCoreV1.PersistentVolumeClaim)
	kind := resource.GetObjectKind().GroupVersionKind().Kind
	if len(req.Namespace) == 0 {
		req.Namespace = "default"
	}
	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":
		client := c.clt.CoreV1().PersistentVolumeClaims(req.Namespace)
		delPolicy := apiMetaV1.DeletePropagationForeground
		if err := client.Delete(req.Name, &apiMetaV1.DeleteOptions{PropagationPolicy: &delPolicy}); err != nil {
			return errors.Wrapf(err, "resource delete failed - kind: %v, name: %v", kind, req.Name)
		}
		log.Printf("resource deleted - kind: %v , name: %v", kind, req.Name)
	default:
		return fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
	return nil
}

func (c *K8s) serviceExists(resource runtime.Object) (bool, error) {
	req := resource.(*apiCoreV1.Service)
	kind := resource.GetObjectKind().GroupVersionKind().Kind
	if len(req.Namespace) == 0 {
		req.Namespace = "default"
	}

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":
		client := c.clt.CoreV1().Services(req.Namespace)
		res, err := client.Get(req.Name, apiMetaV1.GetOptions{})
		if err != nil {
			return false, errors.Wrapf(err, "Checking Service resource status failed")
		}
		if res.Spec.Type == apiCoreV1.ServiceTypeLoadBalancer {
			// k8s API currently just supports LoadBalancerStatus
			if len(res.Status.LoadBalancer.Ingress) > 0 {
				log.Printf("\tService %s Details", req.Name)
				for _, x := range res.Status.LoadBalancer.Ingress {
					log.Printf("\t\thttp://%s:%d", x.IP, res.Spec.Ports[0].Port)
				}
				return true, nil
			}
			return false, nil
		}
		// For any other type we blindly assume that it is up and running as we have no way of checking.
		return true, nil
	default:
		return false, fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
}

func (c *K8s) deploymentReady(resource runtime.Object) (bool, error) {
	req := resource.(*appsV1.Deployment)
	kind := resource.GetObjectKind().GroupVersionKind().Kind
	if len(req.Namespace) == 0 {
		req.Namespace = "default"
	}

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":
		client := c.clt.AppsV1().Deployments(req.Namespace)

		res, err := client.Get(req.Name, apiMetaV1.GetOptions{})
		if err != nil {
			return false, errors.Wrapf(err, "Checking Deployment resource:'%v' status failed err:%v", req.Name, err)
		}

		replicas := int32(1)
		if req.Spec.Replicas != nil {
			replicas = *req.Spec.Replicas
		}
		if res.Status.AvailableReplicas == replicas {
			return true, nil
		}
		return false, nil
	default:
		return false, fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
}

func (c *K8s) daemonsetReady(resource runtime.Object) (bool, error) {
	req := resource.(*appsV1.DaemonSet)
	kind := resource.GetObjectKind().GroupVersionKind().Kind
	if len(req.Namespace) == 0 {
		req.Namespace = "default"
	}

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":
		client := c.clt.AppsV1().DaemonSets(req.Namespace)

		res, err := client.Get(req.Name, apiMetaV1.GetOptions{})
		if err != nil {
			return false, errors.Wrapf(err, "Checking DaemonSet resource:'%v' status failed err:%v", req.Name, err)
		}
		if res.Status.NumberUnavailable == 0 {
			return true, nil
		}
	default:
		return false, fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
	return false, nil
}

func (c *K8s) namespaceDeleted(resource runtime.Object) (bool, error) {
	req := resource.(*apiCoreV1.Namespace)
	kind := resource.GetObjectKind().GroupVersionKind().Kind

	switch v := resource.GetObjectKind().GroupVersionKind().Version; v {
	case "v1":
		client := c.clt.CoreV1().Namespaces()

		if _, err := client.Get(req.Name, apiMetaV1.GetOptions{}); err != nil {
			if apiErrors.IsNotFound(err) {
				return true, nil
			}
			return false, errors.Wrapf(err, "Couldn't get namespace '%v' err:%v", req.Name, err)
		}
		return false, nil
	default:
		return false, fmt.Errorf("unknown object version: %v kind:'%v', name:'%v'", v, kind, req.Name)
	}
}
