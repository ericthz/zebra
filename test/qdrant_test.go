package main

import (
	"context"
	"fmt"
	"log"
	"testing"

	"github.com/qdrant/go-client/qdrant"
)

func TestQdrant(t *testing.T) {
	ctx := context.Background()

	// 创建客户端（无需 ctx 参数）
	client, err := qdrant.NewClient(&qdrant.Config{
		Host: "localhost",
		Port: 6334,
	})
	if err != nil {
		log.Fatalf("无法创建客户端: %v", err)
	}
	defer client.Close()

	collectionName := "demo_collection"
	vectorSize := uint64(4)

	// 检查集合是否存在，不存在则创建
	collections, err := client.ListCollections(ctx)
	if err != nil {
		log.Fatalf("列出集合失败: %v", err)
	}

	exists := false
	for _, name := range collections {
		if name == collectionName {
			exists = true
			break
		}
	}
	if !exists {
		err = client.CreateCollection(ctx, &qdrant.CreateCollection{
			CollectionName: collectionName,
			VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
				Size:     vectorSize,
				Distance: qdrant.Distance_Cosine,
			}),
		})
		if err != nil {
			log.Fatalf("创建集合失败: %v", err)
		}
		fmt.Println("集合创建成功")
	}

	// 插入点（保持不变）
	points := []*qdrant.PointStruct{
		{
			Id:      qdrant.NewIDNum(1),
			Vectors: qdrant.NewVectors(0.1, 0.2, 0.3, 0.4),
			Payload: qdrant.NewValueMap(map[string]any{"city": "北京", "category": "科技"}),
		},
		{
			Id:      qdrant.NewIDNum(2),
			Vectors: qdrant.NewVectors(0.2, 0.1, 0.4, 0.3),
			Payload: qdrant.NewValueMap(map[string]any{"city": "上海", "category": "金融"}),
		},
		{
			Id:      qdrant.NewIDNum(3),
			Vectors: qdrant.NewVectors(0.9, 0.8, 0.7, 0.6),
			Payload: qdrant.NewValueMap(map[string]any{"city": "深圳", "category": "科技"}),
		},
		{
			Id:      qdrant.NewIDNum(4),
			Vectors: qdrant.NewVectors(0.05, 0.15, 0.25, 0.35),
			Payload: qdrant.NewValueMap(map[string]any{"city": "北京", "category": "文化"}),
		},
	}
	client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: collectionName,
		Points:         points,
		Wait:           qdrant.PtrOf(true),
	})

	// ========== 新版 Query 搜索（替代 Search） ==========
	queryVector := []float32{0.1, 0.2, 0.3, 0.4}

	// 不带过滤的搜索
	searchResult, err := client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: collectionName,
		Query:          qdrant.NewQuery(queryVector...), // 使用 NewQuery 传入向量
		Limit:          qdrant.PtrOf(uint64(3)),
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		log.Fatalf("搜索失败: %v", err)
	}

	fmt.Println("\n无过滤的搜索结果：")
	for _, point := range searchResult {
		city := point.Payload["city"].GetStringValue()
		category := point.Payload["category"].GetStringValue()
		fmt.Printf("ID: %d, Score: %.4f, Payload: {city: %s, category: %s}\n",
			point.Id.GetNum(), point.Score, city, category)
	}

	// 带过滤的搜索
	filteredResult, err := client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: collectionName,
		Query:          qdrant.NewQuery(queryVector...),
		Limit:          qdrant.PtrOf(uint64(3)),
		Filter: &qdrant.Filter{
			Must: []*qdrant.Condition{
				qdrant.NewMatch("city", "北京"),
			},
		},
		WithPayload: qdrant.NewWithPayload(true),
	})
	if err != nil {
		log.Fatalf("过滤搜索失败: %v", err)
	}

	fmt.Println("\n过滤后（city=北京）的搜索结果：")
	for _, point := range filteredResult {
		city := point.Payload["city"].GetStringValue()
		category := point.Payload["category"].GetStringValue()
		fmt.Printf("ID: %d, Score: %.4f, Payload: {city: %s, category: %s}\n",
			point.Id.GetNum(), point.Score, city, category)
	}
}
