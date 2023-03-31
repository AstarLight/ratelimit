package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"github.com/go-redis/redis/v8"
)

type RedisClient interface {
	RateDel(context.Context, string) error
	RateEvalSha(context.Context, string, []string, ...interface{}) (interface{}, error)
	RateScriptLoad(context.Context, string) (string, error)
	RateSet(ctx context.Context, key string, max string) error
	RateGet(ctx context.Context, key string) (interface{}, error)
}

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

func (c *redisClient) RateSet(ctx context.Context, key string, max string) error {
	return c.HSet(ctx, key, "lt", max).Err()
}

func (c *redisClient) RateGet(ctx context.Context, key string) (interface{}, error) {
	return c.HMGet(ctx, key, "ct", "lt").Result()
}

// Limiter struct.
type Limiter struct {
	abstractLimiter
}

type abstractLimiter interface {
	getLimit(key string) ([]interface{}, error)
	removeLimit(key, strategy string) error
	setLimit(id, strategy, newLimit string) error
	onlyGet(id, strategy string) (interface{}, error)
}

type LimiterConf struct {
	Max      string // 限流阈值
	Duration string // 时间，单位是毫秒
	Strategy string // 限流策略，如5-S表示1秒允许5个请求
}

// Options for Limiter
type Options struct {
	Ctx    context.Context
	Client RedisClient // Use a redis client for limiter, if omit, it will use a memory limiter.
	Confs  []string
}

// Result of limiter.Get
type Result struct {
	Total      int    // 限流阈值
	Use        int    // 当前已使用的个数
	ReachLimit int    // 是否达到限制
	Strategy   string // uid:5-S，当ReachLimit为1时，我们可以通过这个字段看出是哪个策略拦截
}

// New returns a Limiter instance with given options.
func New(opts Options) *Limiter {
	return newRedisLimiter(&opts)
}

// 将 30-D 这样的格式切为 30 时间 这样的格式返回
func SplitLimitPeriod(str string) (error, string, string) {
	values := strings.Split(str, "-")
	if len(values) != 2 {
		return errors.New(fmt.Sprintf("incorrect format '%s'", str)), "", ""
	}

	periods := map[string]time.Duration{
		"S": time.Second,    // Second
		"M": time.Minute,    // Minute
		"H": time.Hour,      // Hour
		"D": time.Hour * 24, // Day
	}

	limit, period := values[0], strings.ToUpper(values[1])
	p, ok := periods[period]
	if !ok {
		return errors.New(fmt.Sprintf("incorrect period '%s'", period)), "", ""
	}

	_, err := strconv.ParseInt(limit, 10, 64)
	if err != nil {
		return errors.New(fmt.Sprintf("incorrect limit '%s'", limit)), "", ""
	}
	return nil, limit, strconv.FormatInt(int64(p/time.Millisecond), 10)
}

func newRedisLimiter(opts *Options) *Limiter {
	sha1, err := opts.Client.RateScriptLoad(opts.Ctx, lua)
	if err != nil {
		panic(err)
	}

	confs := []*LimiterConf{}
	for _, strategy := range opts.Confs {
		err, limit, period := SplitLimitPeriod(strategy)
		if err != nil {
			panic(err.Error())
			return nil
		}

		conf := &LimiterConf{
			Max:      limit,
			Duration: period,
			Strategy: strategy,
		}

		confs = append(confs, conf)
	}

	r := &redisLimiter{
		rc:    opts.Client,
		sha1:  sha1,
		Confs: confs,
		ctx:   opts.Ctx,
	}
	return &Limiter{r}
}

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
	result.Strategy = res[1].(string)
	return result, nil
}

// Remove remove limiter record for id
func (l *Limiter) Remove(key, strategy string) error {
	return l.removeLimit(key, strategy)
}

func (l *Limiter) Set(key, strategy, newLimit string) error {
	return l.setLimit(key, strategy, newLimit)
}

func (l *Limiter) GetStrategy(key, strategy string) (int, int, error) {
	res, err := l.onlyGet(key, strategy)
	fmt.Println(res)
	arr := res.([]interface{})
	if arr[0] == nil || arr[1] == nil {
		return 0, 0, errors.New("no key")
	}
	use, _ := strconv.Atoi(arr[0].(string))
	total, _ := strconv.Atoi(arr[1].(string))
	return use, total, err
	
}

type redisLimiter struct {
	sha1  string
	rc    RedisClient
	Confs []*LimiterConf
	ctx context.Context
}

func (r *redisLimiter) removeLimit(key, strategy string) error {
	newConfs := []*LimiterConf{}
	for _, conf := range r.Confs {
		if conf.Strategy != strategy {
			newConfs = append(newConfs, conf)
		}
	}
	r.Confs = newConfs
	return r.rc.RateDel(r.ctx, r.getFullKey(key, strategy))
}

func (r *redisLimiter) getFullKey(id, strategy string) string {

	return fmt.Sprintf("%s:%s", id, strategy)
}

// 修改频率上限
func (r *redisLimiter) setLimit(id, strategy, newLimit string) error {
	for i, conf := range r.Confs {
		if conf.Strategy == strategy {
			r.Confs[i].Max = newLimit
			break;
		}
	}
	return r.rc.RateSet(r.ctx, r.getFullKey(id, strategy), newLimit)
}

func (r *redisLimiter) onlyGet(id, strategy string) (interface{}, error) {
	return r.rc.RateGet(r.ctx, r.getFullKey(id, strategy))
}

func (r *redisLimiter) getLimit(key string) ([]interface{}, error) {
	args := make([]interface{}, len(r.Confs)*2, len(r.Confs)*2)
	keys := make([]string, len(r.Confs), len(r.Confs))
	for i, conf := range r.Confs {
		args[i*2] = conf.Max
		args[i*2+1] = conf.Duration

		fullKey := r.getFullKey(key, conf.Strategy)
		keys[i] = fullKey // 238918319:5-M
	}

	fmt.Println(keys)
	fmt.Println(args)
	res, err := r.rc.RateEvalSha(r.ctx, r.sha1, keys, args...)
	if err != nil && isNoScriptErr(err) {
		// try to load lua for cluster client and ring client for nodes changing.
		_, err = r.rc.RateScriptLoad(r.ctx, lua)
		if err == nil {
			res, err = r.rc.RateEvalSha(r.ctx, r.sha1, keys, args...)
		}
	}

	if err == nil {
		arr, ok := res.([]interface{})
		fmt.Println(res)
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
-- KEYS[n] uid556666:50-S, uid556666:100-M, uid556666:2000-H, uid556666:5000-D ...  5000-D means 1day5000limit
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
