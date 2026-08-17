# Bug Reproduction

## 包的性质

当前 test_model_fix 保存的是被测模型修复后的结果源码，不是初始含 Bug 源码。要复现原始缺陷，必须检出下面固定的 parent SHA；不要在当前修复结果源码上期待重新出现修复前失败。生成系统使用的可信验证补丁和完整验证日志仅在本地留存，不提交到结果分支。

## 问题现象

线上出大事了：只要有一票走到签收，整个 arcticfreight 服务就废了，之后所有查询和操作全都不响应，只能重启进程。

签收之前一路都是正常的，状态也对：

```
$ curl -s localhost:51108/api/bookings/booking-2 | grep status
"status":"handed_over"
```

然后签收就再也没回来过：

```
$ curl -sS --max-time 20 -X POST localhost:51108/api/bookings/booking-2/sign -d '{"timestamp":"2026-12-20T10:30:00Z"}'
curl: (28) Operation timed out after 20002 milliseconds with 0 bytes received
http_code=000
elapsed=20s
```

更麻烦的是，从这一刻起别的接口也一起挂了：

```
$ curl -sS --max-time 10 localhost:51108/api/voyages
curl: (28) Operation timed out after 10006 milliseconds with 0 bytes received
http_code=000

$ curl -sS --max-time 10 localhost:51108/api/bookings/booking-2
curl: (28) Operation timed out after 10004 milliseconds with 0 bytes received
http_code=000

$ curl -sS --max-time 10 localhost:51108/health
{"status":"ok"}
http_code=200
```

进程没退、没 panic、CPU 也不高，日志里从头到尾只有一行 listening，一条报错都没有。健康检查还是 200，所以监控完全没报警，我们是被货主投诉才发现的。签收之前的锁舱、确认、装货、起运、卸货、交接每一步都好好的，超温报警那条路也正常。

请先不要修改代码。先帮我把根因定位清楚：讲清楚签收这一步为什么会永远不返回、为什么它一挂之后连只读查询都跟着不响应、以及为什么 /health 反而还能通。给出你实际执行过的复现命令和观察到的输出，以及支撑结论的代码位置。结论确认之后再讨论怎么改。

## 含 Bug 版本

- 仓库：11DingKing/goDing-05
- 仓库地址：https://github.com/11DingKing/goDing-05.git
- parent SHA：909c3b5e70e2d7327c1dca9306b54eb479e4037e

## 复现步骤

```bash
git clone -- https://github.com/11DingKing/goDing-05.git bug-repro
cd bug-repro
git checkout --detach 909c3b5e70e2d7327c1dca9306b54eb479e4037e
go test ./internal/httpapi/ -run "TestHTTPSignReceiptReturnsPromptly|TestHTTPServiceStaysUsableAfterSign|TestHTTPSignReceiptWithBreachReturnsLiability" -count=1 -timeout=120s
```

## 双架构完整错误信息

### linux/amd64

- 容器内复现预期退出码：1
- 容器内复现实际退出码：1

stdout：

```text
$ go test ./internal/httpapi/ -run "TestHTTPSignReceiptReturnsPromptly|TestHTTPServiceStaysUsableAfterSign|TestHTTPSignReceiptWithBreachReturnsLiability" -count=1 -timeout=120s
--- FAIL: TestHTTPSignReceiptReturnsPromptly (5.03s)
    sign_test.go:99: sign receipt did not return within 5s
--- FAIL: TestHTTPServiceStaysUsableAfterSign (5.01s)
    sign_test.go:121: sign receipt did not return within 5s
--- FAIL: TestHTTPSignReceiptWithBreachReturnsLiability (5.00s)
    sign_test.go:163: sign receipt with breach did not return within 5s
FAIL
FAIL	arcticfreight/internal/httpapi	15.131s
FAIL

```

stderr：

```text
(empty)
```

### linux/arm64

- 容器内复现预期退出码：1
- 容器内复现实际退出码：1

stdout：

```text
$ go test ./internal/httpapi/ -run "TestHTTPSignReceiptReturnsPromptly|TestHTTPServiceStaysUsableAfterSign|TestHTTPSignReceiptWithBreachReturnsLiability" -count=1 -timeout=120s
--- FAIL: TestHTTPSignReceiptReturnsPromptly (5.01s)
    sign_test.go:99: sign receipt did not return within 5s
--- FAIL: TestHTTPServiceStaysUsableAfterSign (5.00s)
    sign_test.go:121: sign receipt did not return within 5s
--- FAIL: TestHTTPSignReceiptWithBreachReturnsLiability (5.00s)
    sign_test.go:163: sign receipt with breach did not return within 5s
FAIL
FAIL	arcticfreight/internal/httpapi	15.021s
FAIL

```

stderr：

```text
(empty)
```

## 通过条件

目标仓库零改动（git status 干净，无新增、修改或删除文件）。
准确指出出问题的 Go 文件与具体符号。
说明该符号的错误行为如何造成同一 goroutine 上的锁重入，使签收调用永久阻塞且持有的写锁永不释放，进而让其他读写接口（含只读查询）全部阻塞、请求零字节超时，而不触碰共享状态的 /health 仍能返回 200。
给出实际执行过的复现命令与观察到的输出作为证据，而非仅凭阅读代码推断。
