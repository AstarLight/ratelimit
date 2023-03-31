package main

import (
	"context"
	"github.com/go-redis/redis/v8"
	"log"
	"github.com/gin-gonic/gin"
)

var limiter *Limiter

// Implements RedisClient for redis.Client
type redisClient struct {
	*redis.Client
}

func (c *redisClient) RateDel(ctx context.Context, key string) error {
	return c.Del(ctx, key).Err()
}

func (c *redisClient) RateEvalSha(ctx context.Context, sha1 string, keys []string, args ...interface{}) (interface{}, error) {
	return c.EvalSha(ctx, sha1, keys, args...).Result()
}

func (c *redisClient) RateScriptLoad(ctx context.Context, script string) (string, error) {
	return c.ScriptLoad(ctx, script).Result()
}

func (c *redisClient) RateSet(ctx context.Context, key string, max int) error {
	return c.HSet(ctx, key, "lt", max).Err()
}


//curl "http://localhost:8080/request?username=lijunshi"
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

	r.GET("/request", request)

	r.Run(":8080")
}
