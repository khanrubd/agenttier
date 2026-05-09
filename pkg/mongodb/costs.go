/*
Copyright 2024 AgentTier Authors.

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

package mongodb

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// CostRecord represents an hourly resource consumption snapshot.
type CostRecord struct {
	SandboxID   string        `bson:"sandboxId" json:"sandboxId"`
	Namespace   string        `bson:"namespace" json:"namespace"`
	TemplateRef string        `bson:"templateRef" json:"templateRef"`
	UserID      string        `bson:"userId" json:"userId"`
	Period      time.Time     `bson:"period" json:"period"` // Hourly bucket
	Resources   ResourceUsage `bson:"resources" json:"resources"`
	EstimatedCost CostEstimate `bson:"estimatedCost" json:"estimatedCost"`
	Status      string        `bson:"status" json:"status"` // "running" or "stopped"
}

// ResourceUsage tracks resource consumption for a period.
type ResourceUsage struct {
	CPUHours       float64 `bson:"cpuHours" json:"cpuHours"`
	MemoryGBHours  float64 `bson:"memoryGBHours" json:"memoryGBHours"`
	StorageGBHours float64 `bson:"storageGBHours" json:"storageGBHours"`
}

// CostEstimate holds estimated costs in USD.
type CostEstimate struct {
	Compute float64 `bson:"compute" json:"compute"`
	Storage float64 `bson:"storage" json:"storage"`
	Total   float64 `bson:"total" json:"total"`
}

// CostRates defines the per-unit cost rates for estimation.
type CostRates struct {
	CPUPerHour       float64 // USD per vCPU-hour
	MemoryPerGBHour  float64 // USD per GB-hour
	StoragePerGBHour float64 // USD per GB-hour
}

// DefaultCostRates provides reasonable defaults based on typical cloud pricing.
var DefaultCostRates = CostRates{
	CPUPerHour:       0.04,   // ~$30/month per vCPU
	MemoryPerGBHour:  0.005,  // ~$3.6/month per GB
	StoragePerGBHour: 0.0001, // ~$0.07/month per GB (gp3)
}

// InsertCostRecord stores a cost record for the current period.
func (c *Client) InsertCostRecord(ctx context.Context, record *CostRecord) error {
	_, err := c.Collection(CollCostRecords).InsertOne(ctx, record)
	return err
}

// CostSummary holds aggregated cost data.
type CostSummary struct {
	TotalCompute float64 `json:"totalCompute"`
	TotalStorage float64 `json:"totalStorage"`
	Total        float64 `json:"total"`
	SandboxCount int     `json:"sandboxCount"`
}

// GetCostSummary aggregates costs for a namespace over a time range.
func (c *Client) GetCostSummary(ctx context.Context, namespace string, from, to time.Time) (*CostSummary, error) {
	pipeline := bson.A{
		bson.D{{Key: "$match", Value: bson.D{
			{Key: "namespace", Value: namespace},
			{Key: "period", Value: bson.D{
				{Key: "$gte", Value: from},
				{Key: "$lte", Value: to},
			}},
		}}},
		bson.D{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: nil},
			{Key: "totalCompute", Value: bson.D{{Key: "$sum", Value: "$estimatedCost.compute"}}},
			{Key: "totalStorage", Value: bson.D{{Key: "$sum", Value: "$estimatedCost.storage"}}},
			{Key: "total", Value: bson.D{{Key: "$sum", Value: "$estimatedCost.total"}}},
			{Key: "sandboxCount", Value: bson.D{{Key: "$addToSet", Value: "$sandboxId"}}},
		}}},
	}

	cursor, err := c.Collection(CollCostRecords).Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var results []struct {
		TotalCompute float64  `bson:"totalCompute"`
		TotalStorage float64  `bson:"totalStorage"`
		Total        float64  `bson:"total"`
		SandboxCount []string `bson:"sandboxCount"`
	}
	if err := cursor.All(ctx, &results); err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return &CostSummary{}, nil
	}

	return &CostSummary{
		TotalCompute: results[0].TotalCompute,
		TotalStorage: results[0].TotalStorage,
		Total:        results[0].Total,
		SandboxCount: len(results[0].SandboxCount),
	}, nil
}

// GetCostsByTemplate aggregates costs grouped by template.
func (c *Client) GetCostsByTemplate(ctx context.Context, namespace string, from, to time.Time) ([]TemplateCost, error) {
	pipeline := bson.A{
		bson.D{{Key: "$match", Value: bson.D{
			{Key: "namespace", Value: namespace},
			{Key: "period", Value: bson.D{
				{Key: "$gte", Value: from},
				{Key: "$lte", Value: to},
			}},
		}}},
		bson.D{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$templateRef"},
			{Key: "total", Value: bson.D{{Key: "$sum", Value: "$estimatedCost.total"}}},
			{Key: "sandboxCount", Value: bson.D{{Key: "$addToSet", Value: "$sandboxId"}}},
		}}},
		bson.D{{Key: "$sort", Value: bson.D{{Key: "total", Value: -1}}}},
	}

	cursor, err := c.Collection(CollCostRecords).Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var results []struct {
		Template     string   `bson:"_id"`
		Total        float64  `bson:"total"`
		SandboxCount []string `bson:"sandboxCount"`
	}
	if err := cursor.All(ctx, &results); err != nil {
		return nil, err
	}

	costs := make([]TemplateCost, len(results))
	for i, r := range results {
		costs[i] = TemplateCost{
			Template:     r.Template,
			Total:        r.Total,
			SandboxCount: len(r.SandboxCount),
		}
	}
	return costs, nil
}

// TemplateCost holds cost data for a single template.
type TemplateCost struct {
	Template     string  `json:"template"`
	Total        float64 `json:"total"`
	SandboxCount int     `json:"sandboxCount"`
}

// GetPerSandboxCosts returns cost records for individual sandboxes.
func (c *Client) GetPerSandboxCosts(ctx context.Context, namespace string, limit int64) ([]SandboxCost, error) {
	if limit == 0 {
		limit = 50
	}

	pipeline := bson.A{
		bson.D{{Key: "$match", Value: bson.D{
			{Key: "namespace", Value: namespace},
		}}},
		bson.D{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$sandboxId"},
			{Key: "total", Value: bson.D{{Key: "$sum", Value: "$estimatedCost.total"}}},
			{Key: "templateRef", Value: bson.D{{Key: "$first", Value: "$templateRef"}}},
			{Key: "status", Value: bson.D{{Key: "$last", Value: "$status"}}},
		}}},
		bson.D{{Key: "$sort", Value: bson.D{{Key: "total", Value: -1}}}},
		bson.D{{Key: "$limit", Value: limit}},
	}

	cursor, err := c.Collection(CollCostRecords).Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var results []SandboxCost
	if err := cursor.All(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

// SandboxCost holds cost data for a single sandbox.
type SandboxCost struct {
	SandboxID   string  `bson:"_id" json:"sandboxId"`
	TemplateRef string  `bson:"templateRef" json:"templateRef"`
	Total       float64 `bson:"total" json:"total"`
	Status      string  `bson:"status" json:"status"`
}

// CalculateCost computes the estimated cost for a resource usage snapshot.
func CalculateCost(usage ResourceUsage, rates CostRates) CostEstimate {
	compute := usage.CPUHours*rates.CPUPerHour + usage.MemoryGBHours*rates.MemoryPerGBHour
	storage := usage.StorageGBHours * rates.StoragePerGBHour
	return CostEstimate{
		Compute: compute,
		Storage: storage,
		Total:   compute + storage,
	}
}

// Ensure options is used
var _ = options.Find
