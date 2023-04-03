package main

import (
	"context"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"log"
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
			"msg":    "Rate limit exceeded",
			"Use":    res.Use,    // 已使用的次数
			"Total":  res.Total,  // 周期内总共可使用次数
			"Period": res.Period, // 时间周期, 看出是哪个策略拦截的
		})
		return
	}

}

//curl  -X POST "http://localhost:8080/del_limit?username=lijunshi&period=Minute"
func delLimit(c *gin.Context) {
	key := c.Query("username")
	period := c.Query("period")

	err := limiter.Remove(key, period)
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

//curl  -X POST "http://localhost:8080/set_limit?username=lijunshi&period=Minute&limit=20"
func setLimit(c *gin.Context) {
	key := c.Query("username")
	period := c.Query("period")
	newlimit := c.Query("limit")

	err := limiter.Set(key, period, newlimit)
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

//curl  "http://localhost:8080/get_cnt?username=lijunshi&period=Minute"
func getCnt(c *gin.Context) {
	key := c.Query("username")
	period := c.Query("period")

	use, total, err := limiter.GetNowCnt(key, period)
	if err != nil {
		c.JSON(500, gin.H{
			"msg": err.Error(),
		})
		return
	} else {
		c.JSON(200, gin.H{
			"use":   use,
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

	// 配置的限流策略
	strategies := map[string]int{
		"Second": 5,    // 每秒限制5次请求
		"Minute": 10,   // 每分钟限制10次请求
		"Hour":   1000, // 每小时限制1000次请求
		"Day":    2000, // 天限制2000次请求
	}
	limiter = NewLimiter(Options{
		Client: RedisClient{client},
		Confs:  strategies,
		Ctx:    ctx,
	})

	r := gin.Default()

	r.POST("/request", request)
	r.POST("/del_limit", delLimit)
	r.POST("/set_limit", setLimit)
	r.GET("/get_cnt", getCnt)

	r.Run(":8080")
}
