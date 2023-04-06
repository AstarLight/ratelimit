# ratelimit
rate limiter base on redis


这是一个基于Redis的分布式限流器，核心实现是：
- TTL 命令用于控制周期清空
- HSET/HGET 命令用于管理限流器的配置，比如周期和阈值
- 支持Second/Minute/Hour/Day的一个或多个的配置
- 支持运行时修改和删除限流器的限流阈值配置

设计方案：https://zhuanlan.zhihu.com/p/619530958

分布式限流的例子
```
package main

import (
	"context"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"log"
	ratelimiter "github.com/AstarLight/ratelimit"
)

var limiter *ratelimiter.Limiter

//curl  -X POST "http://localhost:8080/request?username=lijunshi"
func request(c *gin.Context) {
	key := c.Query("username")

	res, err := limiter.Get(key) // 是否能通过限流器
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
	limiter = ratelimiter.NewLimiter(ratelimiter.Options{
		Client: ratelimiter.RedisClient{client},
		Confs:  strategies,
		Ctx:    ctx,
	})

	r := gin.Default()

	r.POST("/request", request)

	r.Run(":8080")
}

```
当请求超过限流阈值时，请求将失败，返回值可以看出是哪个策略拦截的。
```
{"Period":"lijunshi:Minute","Total":10,"Use":10,"msg":"Rate limit exceeded"}
```


当然也支持运行过程中修改我们的限流阈值：
```
	err := limiter.Set(key, period, newlimit) // key 时间周期 限流阈值
	if err != nil {
        // 失败
	} else {
        // 成功
	}
```

支持运行时删除限流策略：
```
	err := limiter.Remove(key, period)
	if err != nil {
        // 失败
	} else {
        // 成功
	}
```

example 参考： https://github.com/AstarLight/ratelimit/blob/main/example/main.go
