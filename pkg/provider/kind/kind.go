// Copyright 2020 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package kind

import (
	"context"
	"fmt"
	"github.com/pkg/errors"
	"github.com/prometheus/test-infra/pkg/provider"
	k8sProvider "github.com/prometheus/test-infra/pkg/provider/k8s"
	"gopkg.in/alecthomas/kingpin.v2"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"sigs.k8s.io/kind/pkg/cluster"
	"sigs.k8s.io/kind/pkg/cmd"
	"strings"
)

type Resource = provider.Resource

// KIND holds the fields used to generate an API request.
type KIND struct {

	// The k8s provider used when we work with the manifest files.
	k8sProvider *k8sProvider.K8s
	// The kind provider used to instantiate a new provider.
	kindProvider *cluster.Provider
	// Final DeploymentFiles files.
	DeploymentFiles []string
	// Final DeploymentVars.
	DeploymentVars map[string]string
	// DeployResource to construct DeploymentVars and DeploymentFiles
	DeploymentResource *provider.DeploymentResource
	// Content bytes after parsing the template variables, grouped by filename.
	kindResources []Resource
	// K8s resource.runtime objects after parsing the template variables, grouped by filename.
	k8sResources []k8sProvider.Resource

	ctx context.Context
	// KIND kuberconfig file
	kubeconfig string
}

// New is the KIND constructor.
func New(dr *provider.DeploymentResource) *KIND {
	return &KIND{
		DeploymentResource: dr,
		kindProvider: cluster.NewProvider(
			cluster.ProviderWithLogger(cmd.NewLogger()),
		),
		ctx:        context.Background(),
		kubeconfig: homedir.HomeDir() + "/.kube/config",
	}
}

// SetupDeploymentResources Sets up DeploymentVars and DeploymentFiles
func (c *KIND) SetupDeploymentResources(*kingpin.ParseContext) error {
	customDeploymentVars := map[string]string{
		"NGINX_SERVICE_TYPE":        "NodePort",
		"LOADGEN_SCALE_UP_REPLICAS": "2",
	}

	c.DeploymentFiles = c.DeploymentResource.DeploymentFiles
	c.DeploymentVars = provider.MergeDeploymentVars(
		c.DeploymentResource.DefaultDeploymentVars,
		customDeploymentVars,
		c.DeploymentResource.FlagDeploymentVars,
	)
	return nil
}

// KINDDeploymentsParse parses the environment/kind deployment files and saves the result as bytes grouped by the filename.
// Any DeploymentVar will be replaced in the resources files following the golang text template format.
func (c *KIND) KINDDeploymentsParse(*kingpin.ParseContext) error {
	if err := c.checkDeploymentVarsAndFiles(); err != nil {
		return err
	}

	deploymentResource, err := provider.DeploymentsParse(c.DeploymentFiles, c.DeploymentVars)
	if err != nil {
		return err
	}
	c.kindResources = deploymentResource
	return nil
}

func (c *KIND) K8SDeploymentsParse(*kingpin.ParseContext) error {
	if err := c.checkDeploymentVarsAndFiles(); err != nil {
		return err
	}

	deploymentResource, err := provider.DeploymentsParse(c.DeploymentFiles, c.DeploymentVars)
	if err != nil {
		return err
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
			c.k8sResources = append(c.k8sResources, k8sProvider.Resource{FileName: deployment.FileName, Objects: k8sObjects})
		}
	}
	return nil
}

// checkDeploymentVarsAndFiles checks whether the requied deployment vars are passed.
func (c *KIND) checkDeploymentVarsAndFiles() error {
	reqDepVars := []string{"CLUSTER_NAME"}
	for _, k := range reqDepVars {
		if v, ok := c.DeploymentVars[k]; !ok || v == "" {
			return fmt.Errorf("missing required %v variable", k)
		}
	}
	if len(c.DeploymentFiles) == 0 {
		return fmt.Errorf("missing deployment file(s)")
	}
	return nil
}

// ClusterCreate create a new cluster or applies changes to an existing cluster.
func (c *KIND) ClusterCreate(*kingpin.ParseContext) error {
	for _, deployment := range c.kindResources {
		CreateWithConfigFile := cluster.CreateWithRawConfig(deployment.Content)

		err := c.kindProvider.Create(c.DeploymentVars["CLUSTER_NAME"], CreateWithConfigFile)
		if err != nil {
			return err
		}
	}
	return nil
}

// ClusterDelete deletes a k8s cluster.
func (c *KIND) ClusterDelete(*kingpin.ParseContext) error {
	err := c.kindProvider.Delete(c.DeploymentVars["CLUSTER_NAME"], c.kubeconfig)
	if err != nil {
		return err
	}
	return nil
}

// NewK8sProvider sets the k8s provider used for deploying k8s manifests.
func (c *KIND) NewK8sProvider(*kingpin.ParseContext) error {
	var err error
	apiConfig, err := clientcmd.LoadFromFile(c.kubeconfig)
	if err != nil {
		return err
	}

	c.k8sProvider, err = k8sProvider.New(c.ctx, apiConfig)
	if err != nil {
		return err
	}
	return nil
}

// ResourceApply calls k8s.ResourceApply to apply the k8s objects in the manifest files.
func (c *KIND) ResourceApply(*kingpin.ParseContext) error {
	if err := c.k8sProvider.ResourceApply(c.k8sResources); err != nil {
		return err
	}
	return nil
}

// ResourceDelete calls k8s.ResourceDelete to apply the k8s objects in the manifest files.
func (c *KIND) ResourceDelete(*kingpin.ParseContext) error {
	if err := c.k8sProvider.ResourceDelete(c.k8sResources); err != nil {
		return err
	}
	return nil
}

// GetDeploymentVars shows deployment variables.
func (c *KIND) GetDeploymentVars(parseContext *kingpin.ParseContext) error {
	fmt.Print("-------------------\n   DeploymentVars   \n------------------- \n")
	for key, value := range c.DeploymentVars {
		fmt.Println(key, ": ", value)
	}
	return nil
}

// Get Prombench services
func (c *KIND) GetKINDService(*kingpin.ParseContext) error {

	var (
		InternalIP string
		NodePort   int32
	)

	pods, err := c.k8sProvider.Clt.CoreV1().Pods("default").List(c.ctx, metav1.ListOptions{LabelSelector: "app=grafana"})
	if k8sErrors.IsNotFound(err) || err != nil {
		return err
	}

	for _, v := range pods.Items {
		node, err := c.k8sProvider.Clt.CoreV1().Nodes().Get(c.ctx, v.Spec.NodeName, metav1.GetOptions{})
		if k8sErrors.IsNotFound(err) || err != nil {
			return err
		}

		for _, v := range node.Status.Addresses {
			if v.Type == "InternalIP" {
				InternalIP = v.Address
			}
		}
	}

	service, err := c.k8sProvider.Clt.CoreV1().Services("default").Get(c.ctx, "grafana", metav1.GetOptions{})
	if k8sErrors.IsNotFound(err) || err != nil {
		return err
	}

	NodePort = service.Spec.Ports[0].NodePort

	fmt.Printf("Grafana: http://%v:%v/grafana\n", InternalIP, NodePort)
	fmt.Printf("Prometheus: http://%v:%v/prometheus-meta\n", InternalIP, NodePort)
	fmt.Printf("Logs: http://%v:%v/grafana/explore\n", InternalIP, NodePort)

	return nil
}
