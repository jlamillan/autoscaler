/*
Copyright 2021 Oracle and/or its affiliates.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package oci

import (
	"context"
	"fmt"
	"github.com/oracle/oci-go-sdk/v43/core"
	"k8s.io/klog/v2"
)

// ShapeGetter returns the oci shape attributes for the pool.
type ShapeGetter interface {
	GetInstancePoolShape(pool *core.InstancePool) (*Shape, error)
}

// ShapeClient is an interface around the GetInstanceConfiguration and ListShapes calls.
type ShapeClient interface {
	GetInstanceConfiguration(context.Context, core.GetInstanceConfigurationRequest) (core.GetInstanceConfigurationResponse, error)
	ListShapes(context.Context, core.ListShapesRequest) (core.ListShapesResponse, error)
}

// ShapeClientImpl is the implementation for fetching shape information.
type ShapeClientImpl struct {
	// Can fetch instance configs (flexible shapes)
	computeMgmtClient core.ComputeManagementClient
	// Can fetch shapes directly
	computeClient core.ComputeClient
}

// GetInstanceConfiguration gets the instance configuration.
func (cc ShapeClientImpl) GetInstanceConfiguration(ctx context.Context, req core.GetInstanceConfigurationRequest) (core.GetInstanceConfigurationResponse, error) {
	return cc.computeMgmtClient.GetInstanceConfiguration(ctx, req)
}

// ListShapes lists the shapes.
func (cc ShapeClientImpl) ListShapes(ctx context.Context, req core.ListShapesRequest) (core.ListShapesResponse, error) {
	return cc.computeClient.ListShapes(ctx, req)
}

// Shape includes the resource attributes of a given shape which should be used
// for constructing node templates.
type Shape struct {
	Name          string
	CPU           float32
	GPU           int
	MemoryInBytes float32
}

// createShapeGetter creates a new oci shape getter.
func createShapeGetter(shapeClient ShapeClient) ShapeGetter {
	return &shapeGetterImpl{
		shapeClient: shapeClient,
		cache:       map[string]*Shape{},
	}
}

type shapeGetterImpl struct {
	shapeClient ShapeClient
	cache       map[string]*Shape
}

func (osf *shapeGetterImpl) GetInstancePoolShape(ip *core.InstancePool) (*Shape, error) {

	shape := &Shape{}

	klog.V(5).Info("fetching shape configuration details for instance-pool " + *ip.Id)

	instanceConfig, err := osf.shapeClient.GetInstanceConfiguration(context.Background(), core.GetInstanceConfigurationRequest{
		InstanceConfigurationId: ip.InstanceConfigurationId,
	})
	if err != nil {
		return nil, err
	}

	if instanceConfig.InstanceDetails == nil {
		return nil, fmt.Errorf("instance configuration details for instance %s has not been set", *ip.Id)
	}

	if instanceDetails, ok := instanceConfig.InstanceDetails.(core.ComputeInstanceDetails); ok {
		// flexible shape use details or look up the static shape details below.
		if instanceDetails.LaunchDetails != nil && instanceDetails.LaunchDetails.ShapeConfig != nil {
			if instanceDetails.LaunchDetails.Shape != nil {
				shape.Name = *instanceDetails.LaunchDetails.Shape
			}
			if instanceDetails.LaunchDetails.ShapeConfig.Ocpus != nil {
				shape.CPU = *instanceDetails.LaunchDetails.ShapeConfig.Ocpus
				// Minimum amount of memory unless explicitly set higher
				shape.MemoryInBytes = *instanceDetails.LaunchDetails.ShapeConfig.Ocpus * 1024 * 1024 * 1024
			}
			if instanceDetails.LaunchDetails.ShapeConfig.MemoryInGBs != nil {
				shape.MemoryInBytes = *instanceDetails.LaunchDetails.ShapeConfig.MemoryInGBs * 1024 * 1024 * 1024
			}
		} else {
			allShapes, _ := osf.shapeClient.ListShapes(context.Background(), core.ListShapesRequest{
				CompartmentId: instanceConfig.CompartmentId,
			})
			for _, nextShape := range allShapes.Items {
				if *nextShape.Shape == *instanceDetails.LaunchDetails.Shape {
					shape.Name = *nextShape.Shape
					if nextShape.Ocpus != nil {
						shape.CPU = *nextShape.Ocpus
					}
					if nextShape.MemoryInGBs != nil {
						shape.MemoryInBytes = *nextShape.MemoryInGBs * 1024 * 1024 * 1024
					}
					if nextShape.Gpus != nil {
						shape.GPU = *nextShape.Gpus
					}
				}
			}
		}
	} else {
		return nil, fmt.Errorf("(compute) instance configuration for instance-pool %s not found", *ip.Id)
	}

	// Didn't find a match
	if shape.Name == "" {
		return nil, fmt.Errorf("shape information for instance-pool %s not found", *ip.Id)
	}
	return shape, nil
}
