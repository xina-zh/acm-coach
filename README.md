# ACM Coach

ACM Coach 是一个面向 **ACM / ICPC 训练队伍** 的智能训练分析系统，用于采集队员的做题数据、统计训练情况，并结合 AI 生成训练分析报告和训练建议。

该系统主要服务于 **训练营管理员 / 教练**，帮助快速了解队员的训练活跃度、做题能力以及训练趋势，从而制定更加合理的训练计划。

---

## 项目背景

在 ACM / ICPC 训练过程中，队员通常会在多个 OJ 平台进行训练，例如：

- Codeforces
- 洛谷
- AtCoder

但这些平台的数据分散在不同网站上，管理员难以快速了解队员整体训练情况，例如：

- 最近训练是否活跃
- 做题难度分布
- AC 率变化
- 长期训练趋势

ACM Coach 的目标是：

> 自动收集训练数据 → 统计分析 → 生成训练报告 → 辅助教练决策

---

## 系统功能

### 1. 队员管理

管理员可以查看训练队成员信息，包括：

- 队员列表
- 队员详细训练情况
- OJ 账号信息

访问地址：


/admin/members


---

### 2. 训练数据采集

系统可以从 OJ 平台采集训练数据，例如：

- Codeforces 提交记录
- 做题结果
- 题目难度

采集程序入口：
backend/cmd/collect_cf

---

### 3. 数据统计与聚合

系统会对训练数据进行统计分析，例如：

- 提交次数
- AC 数量
- AC 率
- 难度分布
- 最近训练活跃度

统计程序入口：
backend/cmd/agg_stats

---

### 4. AI 训练分析

系统可以调用 AI 对训练数据进行分析，生成训练报告，例如：

- 当前训练状态分析
- 能力水平评估
- 个性化训练建议

示例：

近期训练活跃度下降，建议恢复每日训练节奏。
简单题完成率较高，但中等难度题目需要加强。
建议增加 1300~1600 难度区间训练。

---

## 技术架构

系统整体架构如下：
```text
OJ Platforms (Codeforces / 洛谷 / AtCoder)
                │
                ▼
         数据采集程序
                │
                ▼
          PostgreSQL
                │
                ▼
           Go Backend
                │
                ▼
         Admin 管理页面
                │
                ▼
           AI 分析模块
```

数据流程：

1. 从 OJ 平台采集做题记录  
2. 将数据存储到 PostgreSQL  
3. 后端服务进行统计分析  
4. 管理页面展示训练数据  
5. AI 模块生成训练分析报告

---

## 技术栈

### 后端
- Go
- Gin Web Framework

### 数据库
- PostgreSQL

### 缓存
- Redis

### 部署
- Docker
- Docker Compose

### 前端
- HTML Templates

---

## 项目结构
```text
acm-coach/
├── README.md
├── backend/
│   ├── cmd/
│   │   ├── server/        # Web 服务入口
│   │   ├── collect_cf/    # Codeforces 数据采集
│   │   └── agg_stats/     # 数据统计聚合
│   ├── internal/
│   │   ├── render/
│   │   └── view/
│   ├── go.mod
│   └── go.sum
└── docker-compose.yml
```
---

## 快速启动

### 1. 启动数据库和缓存
docker compose up -d

启动后包含：
- PostgreSQL
- Redis
---

### 2. 启动后端服务
进入 backend 目录：
cd backend

运行服务：
go run ./cmd/server

启动成功后访问：
http://localhost:8080/admin/members

---

## 数据采集

采集 Codeforces 数据：
cd backend
go run ./cmd/collect_cf


---

## 数据统计

统计训练数据：
cd backend
go run ./cmd/agg_stats

---

## 项目规划

未来计划增加以下功能：

- [ ] 支持多个 OJ 平台（洛谷 / AtCoder）
- [ ] 自动定时采集训练数据
- [ ] 可视化训练统计图表
- [ ] AI 自动生成训练报告
- [ ] 训练计划推荐系统
- [ ] 队员能力成长分析

---

## 适用场景

该项目适用于：

- ACM / ICPC 训练队管理
- 算法训练营管理
- 个人算法训练数据分析

---

## License

MIT License
