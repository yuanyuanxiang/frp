# FRP Client DLL 导出函数对比

## 三个导出函数

### 1. `Run` - 配置文件模式 + 状态控制

```c
int Run(char* configPath, int* statusPtr);
```

**特点：**
- 使用配置文件（.ini/.yaml/.json/.toml）
- 支持状态控制（RUN/STOP/EXIT）
- 支持多个代理配置
- 两层重连机制

**使用场景：**
- 需要复杂配置（多代理、visitors等）
- 需要动态启停控制
- 使用传统配置文件方式

---

### 2. `RunWithPrivilegeKey` - 参数模式（阻塞）

```c
int RunWithPrivilegeKey(
    char* privilegeKey,
    long timestamp,
    char* serverAddr,
    int serverPort,
    char* user,
    char* proxyName,
    char* proxyType,
    char* localIP,
    int localPort,
    int remotePort,
    char* customDomains,
    char* subDomain,
    int useEncryption,
    int useCompression,
    char* bandwidthLimit,
    char* secretKey,
    char* allowUsers
);
```

**特点：**
- 使用预计算的 privilegeKey（无需配置文件）
- 单个代理配置
- 内部调用 `RunWithPrivilegeKeyControlled`（固定状态为 RUN）
- 函数阻塞直到服务结束（只有内层自动重连）

**实现方式：**
```go
// 内部实现（简化版）
func RunWithPrivilegeKey(...) int {
    status := STATUS_RUN  // 固定状态
    return RunWithPrivilegeKeyControlled(..., &status)
}
```

**使用场景：**
- 服务器动态分配凭证给客户端
- 单一代理场景
- 不需要动态启停（启动后一直运行）
- 简化部署（无需配置文件）
- 最简单的接口

---

### 3. `RunWithPrivilegeKeyControlled` - 参数模式 + 状态控制

```c
int RunWithPrivilegeKeyControlled(
    char* privilegeKey,
    long timestamp,
    char* serverAddr,
    int serverPort,
    char* user,
    char* proxyName,
    char* proxyType,
    char* localIP,
    int localPort,
    int remotePort,
    char* customDomains,
    char* subDomain,
    int useEncryption,
    int useCompression,
    char* bandwidthLimit,
    char* secretKey,
    char* allowUsers,
    int* statusPtr  // 状态控制指针
);
```

**特点：**
- 使用预计算的 privilegeKey
- 单个代理配置
- 支持状态控制（RUN/STOP/EXIT）
- 两层重连机制

**使用场景：**
- 服务器动态分配凭证
- 需要动态启停控制
- 单一代理场景
- 结合了 2 和 1 的优点

---

## 重连机制对比

| 函数 | 外层状态控制 | 内层自动重连 | 实现方式 |
|------|------------|------------|---------|
| `Run` | ✅ 支持 RUN/STOP/EXIT | ✅ 指数退避 | 独立实现 |
| `RunWithPrivilegeKey` | ❌ 无（固定 RUN） | ✅ 指数退避 | 复用 RunWithPrivilegeKeyControlled |
| `RunWithPrivilegeKeyControlled` | ✅ 支持 RUN/STOP/EXIT | ✅ 指数退避 | 完整实现 |

**重连策略**（所有函数统一）：前 3 次 200ms，然后增长到最大 20s

### 重连机制详解

#### 内层自动重连（所有函数都有）

在 `client.Service.keepControllerWorking` 中实现：

```go
wait.BackoffUntil(func() (bool, error) {
    svr.loopLoginUntilSuccess(20*time.Second, false, key, unixTime)
    if svr.ctl != nil {
        <-svr.ctl.Done()
        return false, errors.New("control is closed and try another loop")
    }
    return false, nil
}, wait.NewFastBackoffManager(
    wait.FastBackoffOptions{
        Duration:        time.Second,
        Factor:          2,
        Jitter:          0.1,
        MaxDuration:     20 * time.Second,
        FastRetryCount:  3,              // 前 3 次快速重试
        FastRetryDelay:  200 * time.Millisecond,  // 200ms 延迟
        FastRetryWindow: time.Minute,
        FastRetryJitter: 0.5,
    },
), true, svr.ctx.Done())
```

**重连流程：**
1. 连接断开
2. 前 3 次：200ms 延迟，快速重连
3. 第 4 次开始：1s → 2s → 4s → 8s → 16s → 20s（最大值）
4. 持续以 20s 间隔重连，直到成功或服务停止

#### 外层状态控制（Run 和 RunWithPrivilegeKeyControlled）

通过状态指针动态控制：

```c
int status = STATUS_RUN;  // 启动
// ...
status = STATUS_STOP;     // 停止（可以再次启动）
// ...
status = STATUS_RUN;      // 重新启动
// ...
status = STATUS_EXIT;     // 退出（函数返回）
```

---

## 函数选择指南

### 场景 1: 传统配置文件 + 需要动态控制
**选择：`Run`**

```c
int status = STATUS_RUN;
pthread_t thread;
pthread_create(&thread, NULL, (void*)Run, "/path/to/frpc.ini", &status);

// 动态控制
status = STATUS_STOP;  // 停止
status = STATUS_RUN;   // 重启
status = STATUS_EXIT;  // 退出
```

---

### 场景 2: 服务器分配凭证 + 简单启动
**选择：`RunWithPrivilegeKey`**

```c
// 从服务器获取凭证
char* privilegeKey = getPrivilegeKeyFromServer();
long timestamp = getTimestampFromServer();

// 启动（阻塞，一直运行到服务结束）
int result = RunWithPrivilegeKey(
    privilegeKey, timestamp,
    "frp.example.com", 7000,
    NULL, "proxy1", "tcp", NULL, 22, 6000,
    NULL, NULL, 0, 0, NULL, NULL, NULL
);
```

**适用于：**
- SaaS 服务：服务器控制凭证分配
- 简单场景：启动后不需要停止
- 嵌入式设备：资源受限，简化控制逻辑

---

### 场景 3: 服务器分配凭证 + 需要动态控制
**选择：`RunWithPrivilegeKeyControlled`**

```c
// 从服务器获取凭证
char* privilegeKey = getPrivilegeKeyFromServer();
long timestamp = getTimestampFromServer();

int status = STATUS_RUN;
pthread_t thread;

// 启动线程
pthread_create(&thread, NULL, (void*)RunWithPrivilegeKeyControlled,
    privilegeKey, timestamp,
    "frp.example.com", 7000,
    NULL, "proxy1", "tcp", NULL, 22, 6000,
    NULL, NULL, 0, 0, NULL, NULL, NULL,
    &status  // 状态控制
);

// 动态控制
status = STATUS_STOP;  // 停止
status = STATUS_RUN;   // 重启
status = STATUS_EXIT;  // 退出
```

**适用于：**
- SaaS 服务 + 需要动态管理
- 移动应用：网络切换时需要重启
- 桌面应用：用户界面控制启停

---

## 参数对比

| 特性 | Run | RunWithPrivilegeKey | RunWithPrivilegeKeyControlled |
|------|-----|---------------------|-------------------------------|
| 配置方式 | 配置文件 | 函数参数 | 函数参数 |
| 代理数量 | 多个 | 单个 | 单个 |
| 认证方式 | Token（配置文件） | PrivilegeKey | PrivilegeKey |
| 状态控制 | ✅ | ❌ | ✅ |
| 参数数量 | 2 个 | 17 个 | 18 个 |
| 阻塞行为 | 阻塞直到 EXIT | 阻塞直到结束 | 阻塞直到 EXIT |

---

## 状态转换图

### Run / RunWithPrivilegeKeyControlled

```
         [START]
            |
            v
      [UNKNOWN]
            |
            | status = RUN
            v
         [RUNNING] <--+
            |         |
            | status = STOP
            v         |
        [STOPPED] ----+
            |
            | status = RUN (重启)
            |
            | status = EXIT
            v
          [EXIT]
      (函数返回 0)
```

### RunWithPrivilegeKey

```
         [START]
            |
            v
       [RUNNING]
            |
            | (服务结束或错误)
            v
      (函数返回 0/-1)
```

---

## 错误处理

所有函数的返回值：
- `0`: 成功
- `-1`: 失败

**常见错误：**
- 无效的配置参数
- 无法连接到服务器
- 认证失败（privilegeKey 无效）
- 代理配置错误（例如 TCP 没有 remotePort）

---

## 最佳实践

### 1. 单线程使用
每个函数调用都会阻塞，建议在单独线程中运行：

```c
pthread_t thread;
pthread_create(&thread, NULL, (void*)RunWithPrivilegeKey, ...);
// 主线程继续执行其他任务
pthread_join(thread, NULL);  // 等待结束
```

### 2. 状态控制
使用状态控制时，在单独线程中定期检查业务状态：

```c
// 主线程
while (running) {
    if (needRestart) {
        status = STATUS_STOP;
        sleep(1);
        status = STATUS_RUN;
        needRestart = false;
    }
    sleep(1);
}
status = STATUS_EXIT;
```

### 3. 凭证管理
定期刷新 privilegeKey（如果服务器支持时效性验证）：

```c
// 伪代码
while (true) {
    privilegeKey = fetchPrivilegeKey();
    timestamp = fetchTimestamp();

    if (isExpiringSoon(timestamp)) {
        status = STATUS_STOP;
        sleep(1);
        // 更新参数后重新启动
        status = STATUS_RUN;
    }

    sleep(3600);  // 每小时检查一次
}
```

### 4. 错误恢复
结合状态控制和错误检测：

```c
void* frpc_monitor(void* arg) {
    int retries = 0;
    while (retries < MAX_RETRIES) {
        int result = RunWithPrivilegeKeyControlled(..., &status);

        if (result == 0) {
            // 正常退出
            break;
        } else {
            // 错误，重试
            retries++;
            sleep(5 * retries);  // 指数退避
            status = STATUS_RUN;
        }
    }
    return NULL;
}
```

---

## 总结

| 需求 | 推荐函数 |
|------|---------|
| 使用配置文件 | `Run` |
| 服务器分配凭证，简单启动 | `RunWithPrivilegeKey` |
| 服务器分配凭证，需要动态控制 | `RunWithPrivilegeKeyControlled` |
| 需要多代理配置 | `Run`（使用配置文件） |
| 需要动态启停 | `Run` 或 `RunWithPrivilegeKeyControlled` |
| 最简单的集成 | `RunWithPrivilegeKey` |
| 最灵活的控制 | `RunWithPrivilegeKeyControlled` |
