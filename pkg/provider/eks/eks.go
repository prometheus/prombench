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

package eks

import (
	"context"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	awsSession "github.com/aws/aws-sdk-go/aws/session"
	cloudFormation "github.com/aws/aws-sdk-go/service/cloudformation"
	eks "github.com/aws/aws-sdk-go/service/eks"
	"github.com/pkg/errors"
	k8sProvider "github.com/prometheus/test-infra/pkg/provider/k8s"

	"github.com/prometheus/test-infra/pkg/provider"
	"gopkg.in/alecthomas/kingpin.v2"
	yamlGo "gopkg.in/yaml.v2"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	awsToken "sigs.k8s.io/aws-iam-authenticator/pkg/token"
)

type Resource = provider.Resource

type eksCluster struct {
	Cluster    eks.CreateClusterInput
	NodeGroups []eks.CreateNodegroupInput
}

// EKS holds the fields used to generate an API request.
type EKS struct {
	Auth string

	ClusterName string
	// The eks client used when performing EKS requests.
	clientEKS *eks.EKS
	// The aws session used in abstraction of aws credentials.
	sessionAWS *awsSession.Session
	// The cloudFormation client used when performing CloudFormation requests.
	clientCF *cloudFormation.CloudFormation
	// The k8s provider used when we work with the manifest files.
	k8sProvider *k8sProvider.K8s
	// Final DeploymentFiles files.
	DeploymentFiles []string
	// Final DeploymentVars.
	DeploymentVars map[string]string
	// DeployResource to construct DeploymentVars and DeploymentFiles
	DeploymentResource *provider.DeploymentResource
	// Content bytes after parsing the template variables, grouped by filename.
	eksResources []Resource
	// K8s resource.runtime objects after parsing the template variables, grouped by filename.
	k8sResources []k8sProvider.Resource

	ctx context.Context
}

// New is the EKS constructor
func New(dr *provider.DeploymentResource) *EKS {
	eks := &EKS{
		DeploymentResource: dr,
	}
	return eks
}

// NewEKSClient sets the EKS client used when performing the GKE requests.
func (c *EKS) NewEKSClient(*kingpin.ParseContext) error {
	if c.Auth != "" {
	} else if c.Auth = os.Getenv("AWS_APPLICATION_CREDENTIALS"); c.Auth == "" {
		return errors.Errorf("no auth provided set the auth flag or the AWS_APPLICATION_CREDENTIALS env variable")
	}

	// When the auth variable points to a file
	// put the file content in the variable.
	if content, err := ioutil.ReadFile(c.Auth); err == nil {
		c.Auth = string(content)
	}

	// Check if auth data is base64 encoded and decode it.
	encoded, err := regexp.MatchString("^([A-Za-z0-9+/]{4})*([A-Za-z0-9+/]{3}=|[A-Za-z0-9+/]{2}==)?$", c.Auth)
	if err != nil {
		return err
	}
	if encoded {
		auth, err := base64.StdEncoding.DecodeString(c.Auth)
		if err != nil {
			return errors.Wrap(err, "could not decode auth data")
		}
		c.Auth = string(auth)
	}

	credValue := &credentials.Value{}
	if err = yamlGo.UnmarshalStrict([]byte(c.Auth), credValue); err != nil {
		return errors.Wrap(err, "could not get credential values")
	}

	awsSess := awsSession.Must(awsSession.NewSession(&aws.Config{
		Credentials: credentials.NewStaticCredentialsFromCreds(*credValue),
		Region:      aws.String(c.DeploymentVars["ZONE"]),
	}))

	c.sessionAWS = awsSess

	c.clientEKS = eks.New(awsSess)
	c.clientCF = cloudFormation.New(awsSess)

	c.ctx = context.Background()
	return nil
}

// checkDeploymentVarsAndFiles checks whether the requied deployment vars are passed.
func (c *EKS) checkDeploymentVarsAndFiles() error {
	reqDepVars := []string{"ZONE", "CLUSTER_NAME"}
	for _, k := range reqDepVars {
		if v := c.DeploymentVars[k]; v == "" {
			return fmt.Errorf("missing required %v variable", k)
		}
	}
	if len(c.DeploymentFiles) == 0 {
		return fmt.Errorf("missing deployment file(s)")
	}
	return nil
}

// SetupDeploymentResources Sets up DeploymentVars and DeploymentFiles
func (c *EKS) SetupDeploymentResources(*kingpin.ParseContext) error {
	c.DeploymentFiles = c.DeploymentResource.DeploymentFiles
	c.DeploymentVars = provider.MergeDeploymentVars(
		c.DeploymentResource.DefaultDeploymentVars,
		c.DeploymentResource.FlagDeploymentVars,
	)
	return nil
}

// EKSDeploymentParse parses the cluster/nodegroups deployment file and saves the result as bytes grouped by the filename.
// Any variables passed to the cli will be replaced in the resource files following the golang text template format.
func (c *EKS) EKSDeploymentParse(*kingpin.ParseContext) error {
	if err := c.checkDeploymentVarsAndFiles(); err != nil {
		return err
	}

	subnetIds, err := c.listdownSubnetIds(c.DeploymentVars["CLUSTER_NAME"])

	if err != nil {
		log.Fatalf("Error in listing down subnets: %v", err)
	}

	c.DeploymentVars = provider.MergeDeploymentVars(
		c.DeploymentVars,
		map[string]string{
			"EKS_SUBNET_IDS": strings.Join(subnetIds, c.DeploymentVars["SEPARATOR"]),
		},
	)

	deploymentResource, err := provider.DeploymentsParse(c.DeploymentFiles, c.DeploymentVars)
	if err != nil {
		return fmt.Errorf("Couldn't parse deployment files: %v", err)
	}

	c.eksResources = deploymentResource
	return nil
}

// K8SDeploymentsParse parses the k8s objects deployment files and saves the result as k8s objects grouped by the filename.
// Any variables passed to the cli will be replaced in the resources files following the golang text template format.
func (c *EKS) K8SDeploymentsParse(*kingpin.ParseContext) error {
	if err := c.checkDeploymentVarsAndFiles(); err != nil {
		return err
	}

	deploymentResource, err := provider.DeploymentsParse(c.DeploymentFiles, c.DeploymentVars)
	if err != nil {
		return fmt.Errorf("Couldn't parse deployment files: %v", err)
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

// listdownSubnetIds lists all subnet ids for given cluster name, if vpc for given cluster does not exists it creates vpc with given configuration.
func (c *EKS) listdownSubnetIds(clusterName string) ([]string, error) {
	subnetIds, err := c.showSubnetIds(clusterName)

	if err != nil {
		return []string{}, err
	}

	if len(subnetIds) == 0 {
		subnetIds, err = c.createStack(clusterName)
		if err != nil {
			return []string{}, err
		}
	}

	return subnetIds, err
}

// createStack creates a new vpc and prints desired subnetIds.
func (c *EKS) createStack(clusterName string) ([]string, error) {
	stackName := fmt.Sprintf("%s-vpc", clusterName)
	req := &cloudFormation.CreateStackInput{
		TemplateURL: aws.String("https://amazon-eks.s3.us-west-2.amazonaws.com/cloudformation/2020-07-23/amazon-eks-vpc-sample.yaml"),
		StackName:   aws.String(stackName),
		Tags: []*cloudFormation.Tag{
			{
				Key:   aws.String(fmt.Sprintf("kubernetes.io/cluster/%s", clusterName)),
				Value: aws.String("shared"),
			},
		},
		DisableRollback: aws.Bool(true),
	}

	log.Printf("Stack create request: name:'%s'", *req.StackName)

	_, err := c.clientCF.CreateStack(req)
	if err != nil {
		return nil, fmt.Errorf("Couldn't create stack '%s' ,err: %s", *req.StackName, err)
	}

	err = provider.RetryUntilTrue(
		fmt.Sprintf("creating stack:%v", *req.StackName),
		provider.GlobalRetryCount,
		func() (bool, error) { return c.stackCreated(*req.StackName) },
	)

	if err != nil {
		return nil, fmt.Errorf("creating stack err:%v", err)
	}

	return c.showSubnetIds(clusterName)
}

// deleteStack deletes vpc stack.
func (c *EKS) deleteStack(clusterName string) error {
	stackName := fmt.Sprintf("%s-vpc", clusterName)
	req := &cloudFormation.DeleteStackInput{
		StackName: aws.String(stackName),
	}

	log.Printf("Delete stack request: name: '%s'", *req.StackName)

	_, err := c.clientCF.DeleteStack(req)
	if err != nil {
		return fmt.Errorf("Couldn't delete stack '%s' ,err: %s", *req.StackName, err)
	}

	err = provider.RetryUntilTrue(
		fmt.Sprintf("deleting stack: %s", *req.StackName),
		provider.GlobalRetryCount,
		func() (bool, error) { return c.stackDeleted(*req.StackName) },
	)

	if err != nil {
		return fmt.Errorf("deleting stack err:%v", err)
	}

	return nil
}

// stackCreated checks whether stack has been created.
func (c *EKS) stackCreated(name string) (bool, error) {
	req := &cloudFormation.DescribeStacksInput{
		StackName: aws.String(name),
	}
	stackRes, err := c.clientCF.DescribeStacks(req)
	if err != nil {
		return false, fmt.Errorf("Couldn't get stack status: %v", err)
	}
	if *stackRes.Stacks[0].StackStatus == cloudFormation.StackStatusCreateFailed {
		return false, fmt.Errorf("Stack not in a status to become ready - %s", *stackRes.Stacks[0].StackStatus)
	}
	if *stackRes.Stacks[0].StackStatus == cloudFormation.StackStatusCreateComplete {
		return true, nil
	}
	log.Printf("Stack '%s' status: %s", name, *stackRes.Stacks[0].StackStatus)
	return false, nil
}

// stackDeleted checks whether stack has been deleted.
func (c *EKS) stackDeleted(name string) (bool, error) {
	req := &cloudFormation.DescribeStacksInput{
		StackName: aws.String(name),
	}
	stackRes, err := c.clientCF.DescribeStacks(req)

	if err != nil {

		if err.(awserr.Error).Code() == "ValidationError" {
			return true, nil
		}

		return false, fmt.Errorf("Couldn't get stack status: %v", err)
	}

	if *stackRes.Stacks[0].StackStatus == cloudFormation.StackStatusDeleteFailed {
		return false, fmt.Errorf("Stack delete failed - %s", *stackRes.Stacks[0].StackStatus)
	}

	log.Printf("Stack '%s' status: %s", name, *stackRes.Stacks[0].StackStatus)
	return false, nil
}

// showSubnetIds returns all subnet ids for given cluster configuration.
func (c *EKS) showSubnetIds(clusterName string) ([]string, error) {
	stackName := fmt.Sprintf("%s-vpc", clusterName)
	req := &cloudFormation.DescribeStacksInput{
		StackName: aws.String(stackName),
	}
	stackRes, err := c.clientCF.DescribeStacks(req)

	if awsErr, ok := err.(awserr.Error); ok {
		if awsErr.Code() != "ValidationError" {
			return []string{}, awsErr
		}
	}

	for _, stack := range stackRes.Stacks {
		if *stack.StackStatus == cloudFormation.StackStatusCreateComplete {
			stackOutputs := stack.Outputs
			for _, output := range stackOutputs {
				if *output.OutputKey == "SubnetIds" {
					return strings.Split(*output.OutputValue, ","), nil
				}
			}
		}
	}

	return []string{}, nil
}

// ClusterCreate create a new cluster or applies changes to an existing cluster.
func (c *EKS) ClusterCreate(*kingpin.ParseContext) error {
	req := &eksCluster{}
	for _, deployment := range c.eksResources {

		if err := yamlGo.UnmarshalStrict(deployment.Content, req); err != nil {
			return fmt.Errorf("Error parsing the cluster deployment file %s:%v", deployment.FileName, err)
		}

		log.Printf("Cluster create request: name:'%s'", *req.Cluster.Name)
		_, err := c.clientEKS.CreateCluster(&req.Cluster)
		if err != nil {
			return fmt.Errorf("Couldn't create cluster '%v', file:%v ,err: %v", *req.Cluster.Name, deployment.FileName, err)
		}

		err = provider.RetryUntilTrue(
			fmt.Sprintf("creating cluster:%v", *req.Cluster.Name),
			provider.EKSRetryCount,
			func() (bool, error) { return c.clusterRunning(*req.Cluster.Name) },
		)

		if err != nil {
			return fmt.Errorf("creating cluster err:%v", err)
		}

		for _, nodegroupReq := range req.NodeGroups {
			nodegroupReq.ClusterName = req.Cluster.Name
			log.Printf("Nodegroup create request: NodeGroupName: '%s', ClusterName: '%s'", *nodegroupReq.NodegroupName, *req.Cluster.Name)
			_, err := c.clientEKS.CreateNodegroup(&nodegroupReq)
			if err != nil {
				return fmt.Errorf("Couldn't create nodegroup '%v' for cluster '%v, file:%v ,err: %v", nodegroupReq.NodegroupName, req.Cluster.Name, deployment.FileName, err)
			}

			err = provider.RetryUntilTrue(
				fmt.Sprintf("creating nodegroup:%s for cluster:%s", *nodegroupReq.NodegroupName, *req.Cluster.Name),
				provider.EKSRetryCount,
				func() (bool, error) { return c.nodeGroupCreated(*nodegroupReq.NodegroupName, *req.Cluster.Name) },
			)

			if err != nil {
				return fmt.Errorf("creating nodegroup err:%v", err)
			}
		}
	}
	return nil
}

// ClusterDelete deletes a eks Cluster
func (c *EKS) ClusterDelete(*kingpin.ParseContext) error {
	req := &eksCluster{}
	for _, deployment := range c.eksResources {

		if err := yamlGo.UnmarshalStrict(deployment.Content, req); err != nil {
			return fmt.Errorf("Error parsing the cluster deployment file %s:%v", deployment.FileName, err)
		}

		// To delete a cluster we have to manually delete all cluster
		log.Printf("Removing all nodepools for '%s'", *req.Cluster.Name)

		// Listing all nodepools for cluster
		reqL := &eks.ListNodegroupsInput{
			ClusterName: req.Cluster.Name,
		}

		for {
			resL, err := c.clientEKS.ListNodegroups(reqL)
			if err != nil {
				return fmt.Errorf("listing nodepools err:%v", err)
			}

			for _, nodegroup := range resL.Nodegroups {
				log.Printf("Removing nodepool '%s' in cluster '%s'", *nodegroup, *req.Cluster.Name)

				reqD := eks.DeleteNodegroupInput{
					ClusterName:   req.Cluster.Name,
					NodegroupName: nodegroup,
				}
				_, err := c.clientEKS.DeleteNodegroup(&reqD)
				if err != nil {
					return fmt.Errorf("Couldn't create nodegroup '%v' for cluster '%v ,err: %v", *nodegroup, req.Cluster.Name, err)
				}

				err = provider.RetryUntilTrue(
					fmt.Sprintf("deleting nodegroup:%v for cluster:%v", *nodegroup, *req.Cluster.Name),
					provider.GlobalRetryCount,
					func() (bool, error) { return c.nodeGroupDeleted(*nodegroup, *req.Cluster.Name) },
				)

				if err != nil {
					return fmt.Errorf("deleting nodegroup err:%v", err)
				}
			}

			if resL.NextToken == nil {
				break
			} else {
				reqL.NextToken = resL.NextToken
			}
		}

		reqD := &eks.DeleteClusterInput{
			Name: req.Cluster.Name,
		}

		log.Printf("Removing cluster '%v'", *reqD.Name)
		_, err := c.clientEKS.DeleteCluster(reqD)
		if err != nil {
			return fmt.Errorf("Couldn't delete cluster '%v', file:%v ,err: %v", *req.Cluster.Name, deployment.FileName, err)
		}

		err = provider.RetryUntilTrue(
			fmt.Sprintf("deleting cluster:%v", *reqD.Name),
			provider.GlobalRetryCount,
			func() (bool, error) { return c.clusterDeleted(*reqD.Name) })

		if err != nil {
			return fmt.Errorf("removing cluster err:%v", err)
		}

		log.Printf("Removing subnets related to cluster '%s'", *req.Cluster.Name)
		err = c.deleteStack(*req.Cluster.Name)

		if err != nil {
			log.Fatalf("removing cluster's subnets err:%v", err)
		}
	}
	return nil
}

// clusterRunning checks whether a cluster is in a active state.
func (c *EKS) clusterRunning(name string) (bool, error) {
	req := &eks.DescribeClusterInput{
		Name: aws.String(name),
	}
	clusterRes, err := c.clientEKS.DescribeCluster(req)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok && aerr.Code() == eks.ErrCodeNotFoundException {
			return false, nil
		}
		return false, fmt.Errorf("Couldn't get cluster status: %v", err)
	}
	if *clusterRes.Cluster.Status == eks.ClusterStatusFailed {
		return false, fmt.Errorf("Cluster not in a status to become ready - %s", *clusterRes.Cluster.Status)
	}
	if *clusterRes.Cluster.Status == eks.ClusterStatusActive {
		return true, nil
	}
	log.Printf("Cluster '%v' status: %v", name, *clusterRes.Cluster.Status)
	return false, nil
}

func (c *EKS) clusterDeleted(name string) (bool, error) {
	req := &eks.DescribeClusterInput{
		Name: aws.String(name),
	}
	clusterRes, err := c.clientEKS.DescribeCluster(req)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok && aerr.Code() == eks.ErrCodeResourceNotFoundException {
			return true, nil
		}
		return false, fmt.Errorf("Couldn't get cluster status: %v", err)
	}

	log.Printf("Cluster '%v' status: %v", name, *clusterRes.Cluster.Status)
	return false, nil
}

// NodeGroupCreate creates a new k8s nodegroup in an existing cluster.
func (c *EKS) NodeGroupCreate(*kingpin.ParseContext) error {
	req := &eksCluster{}
	for _, deployment := range c.eksResources {

		if err := yamlGo.UnmarshalStrict(deployment.Content, req); err != nil {
			return fmt.Errorf("Error parsing the cluster deployment file %s:%v", deployment.FileName, err)
		}

		for _, nodegroupReq := range req.NodeGroups {
			nodegroupReq.ClusterName = req.Cluster.Name
			log.Printf("Nodegroup create request: NodeGroupName: '%s', ClusterName: '%s'", *nodegroupReq.NodegroupName, *req.Cluster.Name)
			_, err := c.clientEKS.CreateNodegroup(&nodegroupReq)
			if err != nil {
				return fmt.Errorf("Couldn't create nodegroup '%s' for cluster '%s', file:%v ,err: %v", *nodegroupReq.NodegroupName, *req.Cluster.Name, deployment.FileName, err)
			}

			err = provider.RetryUntilTrue(
				fmt.Sprintf("creating nodegroup:%s for cluster:%s", *nodegroupReq.NodegroupName, *req.Cluster.Name),
				provider.GlobalRetryCount,
				func() (bool, error) { return c.nodeGroupCreated(*nodegroupReq.NodegroupName, *req.Cluster.Name) },
			)

			if err != nil {
				return fmt.Errorf("creating nodegroup err:%v", err)
			}
		}
	}
	return nil
}

// NodeGroupDelete deletes a k8s nodegroup in an existing cluster
func (c *EKS) NodeGroupDelete(*kingpin.ParseContext) error {
	req := &eksCluster{}
	for _, deployment := range c.eksResources {
		if err := yamlGo.UnmarshalStrict(deployment.Content, req); err != nil {
			return fmt.Errorf("Error parsing the cluster deployment file %s:%v", deployment.FileName, err)
		}

		for _, nodegroupReq := range req.NodeGroups {
			nodegroupReq.ClusterName = req.Cluster.Name
			log.Printf("Nodegroup delete request: NodeGroupName: '%s', ClusterName: '%s'", *nodegroupReq.NodegroupName, *req.Cluster.Name)
			reqD := eks.DeleteNodegroupInput{
				ClusterName:   req.Cluster.Name,
				NodegroupName: nodegroupReq.NodegroupName,
			}
			_, err := c.clientEKS.DeleteNodegroup(&reqD)
			if err != nil {
				return fmt.Errorf("Couldn't delete nodegroup '%s' for cluster '%s, file:%v ,err: %v", *nodegroupReq.NodegroupName, *req.Cluster.Name, deployment.FileName, err)
			}
			err = provider.RetryUntilTrue(
				fmt.Sprintf("deleting nodegroup:%s for cluster:%s", *nodegroupReq.NodegroupName, *req.Cluster.Name),
				provider.GlobalRetryCount,
				func() (bool, error) { return c.nodeGroupDeleted(*nodegroupReq.NodegroupName, *req.Cluster.Name) },
			)

			if err != nil {
				return fmt.Errorf("deleting nodegroup err:%v", err)
			}

		}
	}
	return nil
}

func (c *EKS) nodeGroupCreated(nodegroupName, clusterName string) (bool, error) {
	req := &eks.DescribeNodegroupInput{
		ClusterName:   aws.String(clusterName),
		NodegroupName: aws.String(nodegroupName),
	}
	nodegroupRes, err := c.clientEKS.DescribeNodegroup(req)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok && aerr.Code() == eks.ErrCodeNotFoundException {
			return false, nil
		}
		return false, fmt.Errorf("Couldn't get nodegroupname status: %v", err)
	}
	if *nodegroupRes.Nodegroup.Status == eks.NodegroupStatusActive {
		return true, nil
	}

	log.Printf("Nodegroup '%v' for Cluster '%v' status: %v", nodegroupName, clusterName, *nodegroupRes.Nodegroup.Status)
	return false, nil

}

func (c *EKS) nodeGroupDeleted(nodegroupName, clusterName string) (bool, error) {
	req := &eks.DescribeNodegroupInput{
		ClusterName:   aws.String(clusterName),
		NodegroupName: aws.String(nodegroupName),
	}
	nodegroupRes, err := c.clientEKS.DescribeNodegroup(req)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok && aerr.Code() == eks.ErrCodeResourceNotFoundException {
			return true, nil
		}
		return false, fmt.Errorf("Couldn't get nodegroupname status: %v", err)
	}

	log.Printf("Nodegroup '%v' for Cluster '%v' status: %v", nodegroupName, clusterName, *nodegroupRes.Nodegroup.Status)
	return false, nil
}

// AllNodeGroupsRunning returns an error if at least one node pool is not running
func (c *EKS) AllNodeGroupsRunning(*kingpin.ParseContext) error {
	req := &eksCluster{}
	for _, deployment := range c.eksResources {
		if err := yamlGo.UnmarshalStrict(deployment.Content, req); err != nil {
			return fmt.Errorf("Error parsing the cluster deployment file %s:%v", deployment.FileName, err)
		}
		for _, nodegroup := range req.NodeGroups {
			isRunning, err := c.nodeGroupCreated(*nodegroup.NodegroupName, *req.Cluster.Name)
			if err != nil {
				return fmt.Errorf("error fetching nodegroup info")
			}
			if !isRunning {
				return fmt.Errorf("nodepool not running name: %v", *nodegroup.NodegroupName)
			}
		}
	}
	return nil
}

// AllNodeGroupsDeleted returns an error if at least one node pool is not deleted
func (c *EKS) AllNodeGroupsDeleted(*kingpin.ParseContext) error {
	req := &eksCluster{}
	for _, deployment := range c.eksResources {
		if err := yamlGo.UnmarshalStrict(deployment.Content, req); err != nil {
			return fmt.Errorf("Error parsing the cluster deployment file %s:%v", deployment.FileName, err)
		}
		for _, nodegroup := range req.NodeGroups {
			isRunning, err := c.nodeGroupDeleted(*nodegroup.NodegroupName, *req.Cluster.Name)
			if err != nil {
				return fmt.Errorf("error fetching nodegroup info")
			}
			if !isRunning {
				return fmt.Errorf("nodepool not running name: %v", *nodegroup.NodegroupName)
			}
		}
	}
	return nil
}

// EKSK8sToken returns aws iam authenticator token which is used to access eks k8s cluster from outside.
func (c *EKS) EKSK8sToken(clusterName, region string) awsToken.Token {

	gen, err := awsToken.NewGenerator(true, false)

	if err != nil {
		log.Fatalf("Token abstraction error: %v", err)
	}

	opts := &awsToken.GetTokenOptions{
		ClusterID: clusterName,
		Session:   c.sessionAWS,
	}

	tok, err := gen.GetWithOptions(opts)

	if err != nil {
		log.Fatalf("Token abstraction error: %v", err)
	}

	return tok
}

// NewK8sProvider sets the k8s provider used for deploying k8s manifests
func (c *EKS) NewK8sProvider(*kingpin.ParseContext) error {

	clusterName := c.DeploymentVars["CLUSTER_NAME"]
	region := c.DeploymentVars["ZONE"]

	req := &eks.DescribeClusterInput{
		Name: &clusterName,
	}

	rep, err := c.clientEKS.DescribeCluster(req)
	if err != nil {
		return fmt.Errorf("failed to get cluster details: %v", err)
	}

	arnRole := *rep.Cluster.Arn

	caCert, err := base64.StdEncoding.DecodeString(*rep.Cluster.CertificateAuthority.Data)
	if err != nil {
		return fmt.Errorf("failed to decode certificate: %v", err.Error())
	}

	cluster := clientcmdapi.NewCluster()
	cluster.CertificateAuthorityData = []byte(caCert)
	cluster.Server = *rep.Cluster.Endpoint

	clusterContext := clientcmdapi.NewContext()
	clusterContext.Cluster = arnRole
	clusterContext.AuthInfo = arnRole

	authInfo := clientcmdapi.NewAuthInfo()
	authInfo.Token = c.EKSK8sToken(clusterName, region).Token

	config := clientcmdapi.NewConfig()
	config.AuthInfos[arnRole] = authInfo
	config.Contexts[arnRole] = clusterContext
	config.Clusters[arnRole] = cluster
	config.CurrentContext = arnRole
	config.Kind = "Config"
	config.APIVersion = "v1"

	c.k8sProvider, err = k8sProvider.New(c.ctx, config)
	if err != nil {
		return fmt.Errorf("k8s provider error %v", err)
	}

	return nil
}

// ResourceApply calls k8s.ResourceApply to apply the k8s objects in the manifest files.
func (c *EKS) ResourceApply(*kingpin.ParseContext) error {
	if err := c.k8sProvider.ResourceApply(c.k8sResources); err != nil {
		return fmt.Errorf("error while applying a resource err: %v", err)
	}
	return nil
}

// ResourceDelete calls k8s.ResourceDelete to apply the k8s objects in the manifest files.
func (c *EKS) ResourceDelete(*kingpin.ParseContext) error {
	if err := c.k8sProvider.ResourceDelete(c.k8sResources); err != nil {
		return fmt.Errorf("error while deleting objects from a manifest file err: %v", err)
	}
	return nil
}

// GetDeploymentVars shows deployment variables.
func (c *EKS) GetDeploymentVars(*kingpin.ParseContext) error {
	fmt.Print("-------------------\n   DeploymentVars   \n------------------- \n")
	for key, value := range c.DeploymentVars {
		fmt.Println(key, " : ", value)
	}

	return nil
}
