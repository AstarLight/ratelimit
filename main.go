package main

import (
	"context"
	"log"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

var limiter *Limiter

//curl  -X POST "http://localhost:8080/request?username=lijunshi"
func request(c *gin.Context) {
	key := c.Query("username")

	res, err := limiter.Get(key)
	if err != nil {
		c.JSON(500, gin.H{
			"msg": err.Error(),
		})
		return
	}

	if res.ReachLimit == 0 {
		// 尚未达到访问限制
		c.JSON(200, gin.H{
			"msg": "success",
		})

		return
	} else {
		// 达到了访问限制阈值，
		c.JSON(429, gin.H{
			"msg": "Rate limit exceeded",
			"Use": res.Use,
			"Total": res.Total,
			"Strategy":res.Strategy,
		})
		return
	}

}

//curl  -X POST "http://localhost:8080/del_limit?username=lijunshi&strategy=10-M"
func delLimit(c *gin.Context) {
	key := c.Query("username")
	strategy := c.Query("strategy")

	err := limiter.Remove(key, strategy)
	if err != nil {
		c.JSON(500, gin.H{
			"msg": err.Error(),
		})
		return
	} else {
		c.JSON(200, gin.H{
			"msg": "success",
		})
		return
	}
}

//curl  -X POST "http://localhost:8080/set_limit?username=lijunshi&strategy=10-M&limit=20"
func setLimit(c *gin.Context) {
	key := c.Query("username")
	strategy := c.Query("strategy")
	newlimit := c.Query("limit")

	err := limiter.Set(key, strategy, newlimit)
	if err != nil {
		c.JSON(500, gin.H{
			"msg": err.Error(),
		})
		return
	} else {
		c.JSON(200, gin.H{
			"msg": "success",
		})
		return
	}

}

//curl  -X POST "http://localhost:8080/get_strategy?username=lijunshi&strategy=10-M"
func getStrategy(c *gin.Context) {
	key := c.Query("username")
	strategy := c.Query("strategy")

	use, total, err:= limiter.GetStrategy(key, strategy)
	if err != nil {
		c.JSON(500, gin.H{
			"msg": err.Error(),
		})
		return
	} else {
		c.JSON(200, gin.H{
			"use": use,
			"total": total,
		})
		return
	}

}

func main() {
	client := redis.NewClient(&redis.Options{
		Addr: "10.10.40.231:6379",
	})

	ctx := context.Background()

	_, err := client.Ping(ctx).Result()
	if err != nil {
		log.Fatalf("connect redis fail, err:%v", err)
		return
	} else {
		log.Println("connect redis succ")
	}

	// 配置的限流策略格式： "<limit>-<period>"", 
	// periods:
	//
	// * "S": second
	// * "M": minute
	// * "H": hour
	// * "D": day
	//
	// Examples:
	//
	// * 5 reqs/second: "5-S"
	// * 10 reqs/minute: "10-M"
	// * 1000 reqs/hour: "1000-H"
	// * 2000 reqs/day: "2000-D"

	Strategies := []string{"5-S", "10-M", "50-H", "100-D"} // 限流策略配置，可配置一个或多个

	limiter = New(Options{
		Client: &redisClient{client},
		Confs:  Strategies,
		Ctx:    ctx,
	})


	r := gin.Default()

	r.POST("/request", request)
	r.POST("/del_limit", delLimit)
	r.POST("/set_limit", setLimit)
	r.POST("/get_strategy", getStrategy)

	r.Run(":8080")
}
