package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-redis/redis/v8"
	"strconv"
	"strings"
	"time"
)

var gPeriods = map[string]time.Duration{
	"Second": time.Second,    // Second
	"Minute": time.Minute,    // Minute
	"Hour":   time.Hour,      // Hour
	"Day":    time.Hour * 24, // Day
}

var (
	errInvalidPeriod = errors.New("invalid period")
)

// Implements RedisClient for redis.Client
type RedisClient struct {
	*redis.Client
}

// Limiter struct.
type Limiter struct {
	sha1  string
	rc    RedisClient
	Confs map[string]*LimiterConf
	ctx   context.Context
}

type LimiterConf struct {
	Max      string // 限流阈值
	Duration string // 时间，单位是毫秒
	Period   string // 限流策略周期，如Second表示1秒内   Second/Minute/Hour/Day
}

// Options for Limiter
type Options struct {
	Ctx    context.Context
	Client RedisClient
	Confs  map[string]int // key: Second/Minute/Hour/Day  val: limit value
}

// Result of limiter.Get
type Result struct {
	Total      int    // 限流阈值, 当ReachLimit为1时生效
	Use        int    // 当前已使用的个数, 当ReachLimit为1时生效
	ReachLimit int    // 是否达到限制
	Period     string // Second/Minute/Hour/Day，当ReachLimit为1时生效，我们可以通过这个字段看出是哪个策略拦截
}

func NewLimiter(opts Options) *Limiter {
	sha1, err := opts.Client.RateScriptLoad(opts.Ctx, lua)
	if err != nil {
		panic(err)
	}

	confs := map[string]*LimiterConf{}
	for period, limit := range opts.Confs {
		p, ok := gPeriods[period]

		if !ok {
			panic("invalid period conf")
		}
		conf := &LimiterConf{
			Max:      fmt.Sprintf("%d", limit),
			Duration: fmt.Sprintf("%d", int64(p/time.Millisecond)),
			Period:   period,
		}

		confs[period] = conf
	}

	r := &Limiter{
		rc:    opts.Client,
		sha1:  sha1,
		Confs: confs,
		ctx:   opts.Ctx,
	}
	return r
}

func (c *RedisClient) RateDel(ctx context.Context, key string) error {
	return c.Del(ctx, key).Err()
}

func (c *RedisClient) RateEvalSha(ctx context.Context, sha1 string, keys []string, args ...interface{}) (interface{}, error) {
	return c.EvalSha(ctx, sha1, keys, args...).Result()
}

func (c *RedisClient) RateScriptLoad(ctx context.Context, script string) (string, error) {
	return c.ScriptLoad(ctx, script).Result()
}

func (c *RedisClient) RateSet(ctx context.Context, key string, max string) error {
	return c.HSet(ctx, key, "lt", max).Err()
}

func (c *RedisClient) RateGet(ctx context.Context, key string) (interface{}, error) {
	return c.HMGet(ctx, key, "ct", "lt").Result()
}

func IsValidPeriod(period string) bool {
	_, ok := gPeriods[period]

	if !ok {
		return false
	}

	return true
}

// Get而且会自增
func (l *Limiter) Get(id string) (Result, error) {
	var result Result
	key := id

	res, err := l.getLimit(key)
	if err != nil {
		return result, err
	}

	result = Result{}
	result.Use = int(res[2].(int64))
	result.Total = int(res[3].(int64))
	result.ReachLimit = int(res[0].(int64))
	result.Period = res[1].(string)
	return result, nil
}

// 删除本地和redis hash 里的限流器
func (l *Limiter) Remove(key, period string) error {
	if !IsValidPeriod(period) {
		return errInvalidPeriod
	}
	delete(l.Confs, period)
	return l.rc.RateDel(l.ctx, l.getFullKey(key, period))
}

// 修改本地和redis hash 里的limit
func (l *Limiter) Set(key, period, newLimit string) error {
	if !IsValidPeriod(period) {
		return errInvalidPeriod
	}
	if _, ok := l.Confs[period]; !ok {
		p, _ := gPeriods[period]
		conf := &LimiterConf{
			Max:      newLimit,
			Duration: fmt.Sprintf("%d", int64(p/time.Millisecond)),
			Period:   period,
		}

		l.Confs[period] = conf
		return nil
	}
	l.Confs[period].Max = newLimit
	return l.rc.RateSet(l.ctx, l.getFullKey(key, period), newLimit)
}

// 只是Get数据，不会自增
func (l *Limiter) GetNowCnt(key, period string) (int, int, error) {
	if !IsValidPeriod(period) {
		return 0, 0, errInvalidPeriod
	}
	res, err := l.getPeriod(key, period)
	arr := res.([]interface{})
	if arr[0] == nil || arr[1] == nil {
		return 0, 0, errors.New("no key")
	}
	use, _ := strconv.Atoi(arr[0].(string))
	total, _ := strconv.Atoi(arr[1].(string))
	return use, total, err

}


func (l *Limiter) getFullKey(id, period string) string {
	return fmt.Sprintf("%s:%s", id, period)
}


func (l *Limiter) getPeriod(id, period string) (interface{}, error) {
	return l.rc.RateGet(l.ctx, l.getFullKey(id, period))
}

func (l *Limiter) getLimit(key string) ([]interface{}, error) {
	args := make([]interface{}, len(l.Confs)*2, len(l.Confs)*2)
	keys := make([]string, len(l.Confs), len(l.Confs))
	i := 0
	for Period, conf := range l.Confs {
		args[i*2] = conf.Max
		args[i*2+1] = conf.Duration

		fullKey := l.getFullKey(key, Period)
		keys[i] = fullKey // 238918319:M
		i += 1
	}

	//fmt.Println(keys)
	//fmt.Println(args)
	res, err := l.rc.RateEvalSha(l.ctx, l.sha1, keys, args...)
	if err != nil && isNoScriptErr(err) {
		// try to load lua for cluster client and ring client for nodes changing.
		_, err = l.rc.RateScriptLoad(l.ctx, lua)
		if err == nil {
			res, err = l.rc.RateEvalSha(l.ctx, l.sha1, keys, args...)
		}
	}

	if err == nil {
		arr, ok := res.([]interface{})
		//fmt.Println(res)
		if ok && len(arr) == 4 {
			return arr, nil
		}
		err = errors.New("Invalid result")
	}
	return nil, err
}

func isNoScriptErr(err error) bool {
	return strings.HasPrefix(err.Error(), "NOSCRIPT ")
}

const lua string = `
-- KEYS[n] uid556666:Second, uid556666:Minute, uid556666:Hour, uid556666:Day ...  uid556666:Minute means 1minute ratelimit
-- ARGV[n] max count, duration, max count, duration, ...
-- HASH: KEYS[n]
--   field:ct(count)
--   field:lt(limit)
local res = {}
local keyCount = #KEYS
local argvCount = #ARGV
local key = ""
for i = 1, #KEYS do
  local maxLimit = ARGV[(i-1)*2+1]
  local duration = ARGV[(i-1)*2+2]
  key = KEYS[i]
  local limit = redis.call('hmget', key, 'ct', 'lt')
  if limit[1] then
    res[1] = tonumber(limit[1])
    res[2] = tonumber(limit[2])
    if res[1] >= res[2] then
      return {1, key, res[1], res[2]} 
	
	else
      redis.call('hincrby', key, 'ct', 1)
    end
  else
    local total = tonumber(maxLimit)
    res[1] = 1
    res[2] = total
    local expire = tonumber(duration)
    redis.call('hmset', key, 'ct', res[1], 'lt', res[2])
    redis.call('pexpire', key, expire)
  end
end
return {0, "", 0, 0} 
`
