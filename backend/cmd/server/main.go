package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

"acmcoach/internal/render"	

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Member struct {
	ID        int64
	Name      string
	Grade     string
	StudentID string
}

type Stat struct {
	WindowDays  int
	AsOfDate    time.Time
	SubmitCount int
	AcCount     int
	AcRate      float64
	ActiveDays  int
}

type SubmissionRow struct {
	Platform      string
	SubmissionID  int64
	ProblemID     string
	Verdict       string
	SubmittedAt   time.Time
	ProblemName   string
	ProblemIndex  string
	ProblemRating *int
}

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:5432/acmcoach?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		panic(err)
	}
	defer pool.Close()

	r := gin.Default()
	r.LoadHTMLGlob("internal/view/templates/*.html")

	r.GET("/health", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	// 成员列表
	r.GET("/admin/members", func(c *gin.Context) {
		rows, err := pool.Query(c, `SELECT id, name, grade, COALESCE(student_id,'') FROM members ORDER BY id`)
		if err != nil {
			c.String(500, err.Error())
			return
		}
		defer rows.Close()

		var ms []Member
		for rows.Next() {
			var m Member
			if err := rows.Scan(&m.ID, &m.Name, &m.Grade, &m.StudentID); err != nil {
				c.String(500, err.Error())
				return
			}
			ms = append(ms, m)
		}
		c.HTML(200, "members.html", gin.H{"Members": ms})
	})

	// 成员详情：stats + 最近提交
	r.GET("/admin/members/:id", func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)

		var m Member
		err := pool.QueryRow(c, `SELECT id, name, grade, COALESCE(student_id,'') FROM members WHERE id=$1`, id).
			Scan(&m.ID, &m.Name, &m.Grade, &m.StudentID)
		if err != nil {
			c.String(404, "member not found")
			return
		}

		// stats
		sRows, err := pool.Query(c, `
			SELECT window_days, as_of_date, submit_count, ac_count, ac_rate, active_days
			FROM member_stats
			WHERE member_id=$1
			ORDER BY window_days
		`, id)
		if err != nil {
			c.String(500, err.Error())
			return
		}
		defer sRows.Close()

		var stats []Stat
		for sRows.Next() {
			var s Stat
			if err := sRows.Scan(&s.WindowDays, &s.AsOfDate, &s.SubmitCount, &s.AcCount, &s.AcRate, &s.ActiveDays); err != nil {
				c.String(500, err.Error())
				return
			}
			stats = append(stats, s)
		}

		// 最近提交（取该 member 所有 codeforces account）
		subRows, err := pool.Query(c, `
			SELECT s.platform, s.submission_id, s.problem_id, s.verdict, s.submitted_at,
			       COALESCE(s.problem_name,''), COALESCE(s.problem_index,''), s.problem_rating
			FROM submissions s
			JOIN accounts a ON a.id = s.account_id
			WHERE a.member_id=$1
			ORDER BY s.submitted_at DESC
			LIMIT 20
		`, id)
		if err != nil {
			c.String(500, err.Error())
			return
		}
		defer subRows.Close()

		var subs []SubmissionRow
		for subRows.Next() {
			var s SubmissionRow
			if err := subRows.Scan(&s.Platform, &s.SubmissionID, &s.ProblemID, &s.Verdict, &s.SubmittedAt,
				&s.ProblemName, &s.ProblemIndex, &s.ProblemRating); err != nil {
				c.String(500, err.Error())
				return
			}
			subs = append(subs, s)
		}

		// latest ai report
		var latestReport string
		var latestModel string
		var latestDate string
		_ = pool.QueryRow(c, `
	SELECT report_md, model, as_of_date::text
	FROM ai_reports
	WHERE member_id=$1
	ORDER BY as_of_date DESC, created_at DESC
	LIMIT 1
`, id).Scan(&latestReport, &latestModel, &latestDate)

		latestReportHTML, err := render.MarkdownToSafeHTML(latestReport)
		if err != nil {
			c.String(500, err.Error())
			return
		}

		backURL := c.Request.Referer()
		if backURL == "" {
			backURL = "/admin/members"
		}

		c.HTML(200, "member_detail.html", gin.H{
			"Member": m,
			"Stats":  stats,
			"Subs":   subs,

			"LatestReportHTML":  latestReportHTML,
			"LatestReportModel": latestModel,
			"LatestReportDate":  latestDate,
		})

	})

	r.GET("/admin", func(c *gin.Context) {
	rows, err := pool.Query(c, `
		SELECT
			m.id,
			m.name,
			COUNT(DISTINCT s.platform || ':' || s.problem_id) AS solved_count
		FROM submissions s
		JOIN accounts a ON s.account_id = a.id
		JOIN members m ON a.member_id = m.id
		WHERE s.verdict = 'OK'
		GROUP BY m.id, m.name
		ORDER BY solved_count DESC, m.id ASC
		LIMIT 20
	`)
	if err != nil {
		c.String(500, err.Error())
		return
	}
	defer rows.Close()

	type Item struct {
		Rank        int
		MemberID    int64
		Name        string
		SolvedCount int
	}

	var items []Item
	rank := 1

	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.MemberID, &it.Name, &it.SolvedCount); err != nil {
			c.String(500, err.Error())
			return
		}
		it.Rank = rank
		rank++
		items = append(items, it)
	}

	c.HTML(200, "admin_home.html", gin.H{
		"Items": items,
	})
})

	r.GET("/admin/rank/daily", func(c *gin.Context) {
   	 	dailyRankHandler(c, pool)
	})
	
	// 手动采集：采集该 member 所有 codeforces 账号
	r.POST("/admin/members/:id/collect", func(c *gin.Context) {
		memberID, _ := strconv.ParseInt(c.Param("id"), 10, 64)

		_, err := collectCFForMember(c, pool, memberID)
		if err != nil {
			c.String(500, err.Error())
			return
		}

		c.Redirect(http.StatusFound, "/admin/members/"+c.Param("id"))
	})
 	
	r.POST("/admin/members/collect-all", func(c *gin.Context) {
   		 collectAllMembers(c, pool)
	})

	r.POST("/admin/members/:id/ai", func(c *gin.Context) {
		memberID, _ := strconv.ParseInt(c.Param("id"), 10, 64)

		reportMD, inputJSON, modelName, err := generateAIPlanDeepSeek(c, pool, memberID)
		if err != nil {
			c.String(500, err.Error())
			return
		}

		_, err = pool.Exec(c, `
        INSERT INTO ai_reports (member_id, as_of_date, model, input_summary, report_md)
        VALUES ($1, CURRENT_DATE, $2, $3::jsonb, $4)
    `, memberID, modelName, inputJSON, reportMD)
		if err != nil {
			c.String(500, err.Error())
			return
		}

		c.Redirect(http.StatusFound, "/admin/members/"+c.Param("id"))
	})

	// 手动聚合：更新该 member 的 member_stats
	r.POST("/admin/members/:id/agg", func(c *gin.Context) {
		memberID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		if err := upsertStatsForMember(c, pool, memberID, time.Now().UTC()); err != nil {
			c.String(500, err.Error())
			return
		}
		c.Redirect(http.StatusFound, "/admin/members/"+c.Param("id"))
	})
	_ = r.Run(":8080")
}

type cfResp[T any] struct {
	Status  string `json:"status"`
	Comment string `json:"comment"`
	Result  T      `json:"result"`
}

type cfSub struct {
	ID                  int64  `json:"id"`
	ContestID           int64  `json:"contestId"`
	CreationTimeSeconds int64  `json:"creationTimeSeconds"`
	Verdict             string `json:"verdict"`
	ProgrammingLanguage string `json:"programmingLanguage"`
	Problem             struct {
		Name   string `json:"name"`
		Index  string `json:"index"`
		Rating *int   `json:"rating"`
	} `json:"problem"`
}

func collectCFForMember(ctx context.Context, pool *pgxpool.Pool, memberID int64) (int, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, handle
		FROM accounts
		WHERE member_id=$1 AND platform='codeforces'
		ORDER BY id
	`, memberID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type acct struct {
		ID     int64
		Handle string
	}
	var accts []acct
	for rows.Next() {
		var a acct
		if err := rows.Scan(&a.ID, &a.Handle); err != nil {
			return 0, err
		}
		accts = append(accts, a)
	}
	if len(accts) == 0 {
		return 0, nil
	}

	client := &http.Client{Timeout: 15 * time.Second}
	insertedTotal := 0

	for _, a := range accts {
		var maxID int64
		_ = pool.QueryRow(ctx, `
			SELECT COALESCE(MAX(submission_id), 0)
			FROM submissions
			WHERE platform='codeforces' AND account_id=$1
		`, a.ID).Scan(&maxID)

		url := "https://codeforces.com/api/user.status?handle=" + a.Handle + "&from=1&count=200"
		req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
		resp, err := client.Do(req)
		if err != nil {
			return insertedTotal, err
		}

		var out cfResp[[]cfSub]
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			_ = resp.Body.Close()
			return insertedTotal, err
		}
		_ = resp.Body.Close()

		if out.Status != "OK" {
			return insertedTotal, fmt.Errorf("codeforces api error: %s", out.Comment)
		}

		for _, s := range out.Result {
			if s.ID <= maxID {
				continue
			}
			problemID := fmt.Sprintf("%d%s", s.ContestID, s.Problem.Index)

			var rating any
			if s.Problem.Rating != nil {
				rating = *s.Problem.Rating
			} else {
				rating = nil
			}

			_, err := pool.Exec(ctx, `
				INSERT INTO submissions (
					account_id, platform, submission_id,
					problem_id, contest_id,
					verdict, language, submitted_at, problem_rating, problem_name, problem_index
				) VALUES (
					$1, 'codeforces', $2,
					$3, $4,
					$5, $6, $7, $8, $9, $10
				)
				ON CONFLICT (platform, submission_id) DO NOTHING
			`,
				a.ID, s.ID,
				problemID, fmt.Sprintf("%d", s.ContestID),
				s.Verdict, s.ProgrammingLanguage,
				time.Unix(s.CreationTimeSeconds, 0).UTC(),
				rating, s.Problem.Name, s.Problem.Index,
			)
			if err != nil {
				return insertedTotal, err
			}
			insertedTotal++
		}
	}

	return insertedTotal, nil
}

func collectAllMembers(c *gin.Context, pool *pgxpool.Pool) {
	rows, err := pool.Query(c, `SELECT id FROM members`)
	if err != nil {
		c.String(500, "query members failed: %v", err)
		return
	}
	defer rows.Close()

	totalMembers := 0
	totalImported := 0

	for rows.Next() {
		var memberID int64
		if err := rows.Scan(&memberID); err != nil {
			c.String(500, "scan member failed: %v", err)
			return
		}

		n, err := collectCFForMember(c, pool, memberID)
		if err != nil {
			continue
		}

		totalMembers++
		totalImported += n
	}

	c.JSON(200, gin.H{
		"members_processed": totalMembers,
		"submissions_added": totalImported,
	})
}

func upsertStatsForMember(ctx context.Context, pool *pgxpool.Pool, memberID int64, asOf time.Time) error {
	_, err := pool.Exec(ctx, `
WITH windows AS (
  SELECT $1::bigint AS member_id, w AS window_days, $2::date AS as_of_date
  FROM (VALUES (7), (30), (120)) AS t(w)
),
range AS (
  SELECT
    member_id,
    window_days,
    as_of_date,
    (as_of_date - (window_days || ' days')::interval) AS start_ts,
    (as_of_date + interval '1 day') AS end_ts
  FROM windows
),
agg AS (
  SELECT
    r.member_id,
    r.window_days,
    r.as_of_date,
    COUNT(s.*)::int AS submit_count,
    COUNT(s.*) FILTER (WHERE s.verdict='OK')::int AS ac_count,
    CASE WHEN COUNT(s.*)=0 THEN 0
         ELSE (COUNT(s.*) FILTER (WHERE s.verdict='OK'))::float / COUNT(s.*)
    END AS ac_rate,
    COUNT(DISTINCT s.submitted_at::date)::int AS active_days
  FROM range r
  LEFT JOIN accounts a ON a.member_id = r.member_id AND a.platform='codeforces'
  LEFT JOIN submissions s ON s.account_id = a.id
                         AND s.submitted_at >= r.start_ts
                         AND s.submitted_at <  r.end_ts
  GROUP BY r.member_id, r.window_days, r.as_of_date
)
INSERT INTO member_stats (member_id, window_days, as_of_date, submit_count, ac_count, ac_rate, active_days)
SELECT member_id, window_days, as_of_date, submit_count, ac_count, ac_rate, active_days
FROM agg
ON CONFLICT (member_id, window_days, as_of_date)
DO UPDATE SET
  submit_count = EXCLUDED.submit_count,
  ac_count = EXCLUDED.ac_count,
  ac_rate = EXCLUDED.ac_rate,
  active_days = EXCLUDED.active_days
`, memberID, asOf)

	return err
}

type dsMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type dsReq struct {
	Model     string      `json:"model"`
	Messages  []dsMessage `json:"messages"`
	Stream    bool        `json:"stream"`
	MaxTokens int         `json:"max_tokens,omitempty"`
}

type dsResp struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func generateAIPlanDeepSeek(ctx context.Context, pool *pgxpool.Pool, memberID int64) (reportMD string, inputJSON string, modelName string, err error) {
	modelName = "deepseek-chat"

	// 1) member
	var name, grade, studentID string
	if e := pool.QueryRow(ctx, `SELECT name, COALESCE(grade,''), COALESCE(student_id,'') FROM members WHERE id=$1`, memberID).
		Scan(&name, &grade, &studentID); e != nil {
		return "", "", modelName, fmt.Errorf("member not found: %w", e)
	}

	// 2) stats（7/30/120）
	type statRow struct {
		WindowDays  int     `json:"window_days"`
		SubmitCount int     `json:"submit_count"`
		AcCount     int     `json:"ac_count"`
		AcRate      float64 `json:"ac_rate"`
		ActiveDays  int     `json:"active_days"`
		AsOfDate    string  `json:"as_of_date"`
	}
	stats := []statRow{}
	sRows, e := pool.Query(ctx, `
		SELECT window_days, submit_count, ac_count, ac_rate, active_days, as_of_date::text
		FROM member_stats
		WHERE member_id=$1
		ORDER BY window_days
	`, memberID)
	if e != nil {
		return "", "", modelName, e
	}
	for sRows.Next() {
		var r statRow
		if e := sRows.Scan(&r.WindowDays, &r.SubmitCount, &r.AcCount, &r.AcRate, &r.ActiveDays, &r.AsOfDate); e != nil {
			sRows.Close()
			return "", "", modelName, e
		}
		stats = append(stats, r)
	}
	sRows.Close()

	// 3) recent submissions（最多 50）
	type subRow struct {
		SubmittedAt  string `json:"submitted_at"`
		ProblemID    string `json:"problem_id"`
		Verdict      string `json:"verdict"`
		Rating       *int   `json:"rating,omitempty"`
		ProblemIndex string `json:"index,omitempty"`
	}
	subs := []subRow{}
	subRows, e := pool.Query(ctx, `
		SELECT to_char(s.submitted_at, 'YYYY-MM-DD HH24:MI:SS') AS submitted_at,
		       s.problem_id, COALESCE(s.verdict,''), s.problem_rating, COALESCE(s.problem_index,'')
		FROM submissions s
		JOIN accounts a ON a.id = s.account_id
		WHERE a.member_id=$1
		ORDER BY s.submitted_at DESC
		LIMIT 50
	`, memberID)
	if e != nil {
		return "", "", modelName, e
	}
	for subRows.Next() {
		var r subRow
		if e := subRows.Scan(&r.SubmittedAt, &r.ProblemID, &r.Verdict, &r.Rating, &r.ProblemIndex); e != nil {
			subRows.Close()
			return "", "", modelName, e
		}
		subs = append(subs, r)
	}
	subRows.Close()

	// 4) input_summary JSON（落库用）
	inputObj := map[string]any{
		"member": map[string]any{
			"id":         memberID,
			"name":       name,
			"grade":      grade,
			"student_id": studentID,
		},
		"stats":              stats,
		"recent_submissions": subs,
		"now_date":           time.Now().Format("2006-01-02"),
	}
	b, _ := json.Marshal(inputObj)
	inputJSON = string(b)

	// 5) Prompt：让输出是可执行训练计划（Markdown）

	system := `你是一名经验丰富的 ACM 算法竞赛教练。
你需要根据学生的训练数据，输出客观、具体、可执行的分析报告。
要求：
1. 输出使用 Markdown 格式。
2. 分析必须尽量基于给定数据，不要编造数据中没有体现的信息。
3. 如果某些结论无法直接从数据判断，要明确说明“无法仅根据当前数据判断”。
4. 训练计划要具体到每天/每周，便于直接执行。
5. 使用简体中文回答。
6. 开头不介绍自己，给前言什么的，直接分析即可

`

	user := fmt.Sprintf(`
下面是某 ACM 队员的训练数据（JSON）：

%s

请按以下结构输出分析报告：

# 1. 当前训练情况分析 （150~200字）
请分析：
- 做题活跃度
- 大致实力水平

# 2. 主要问题   (150~200字)
找出 2-4 个主要问题，并说明判断依据。
例如可以从这些角度分析：
- 做题频率是否稳定
- 提交后是否存在较多错误尝试
- 是否存在某类算法明显薄弱
- 难度提升是否合理

# 3. 7天训练计划 （200~300字）
请按“第1天 ~ 第7天”列出训练安排。每天包含：
- 建议做题数量
- 题目难度范围（Codeforces rating）
- 建议训练内容
- 预计训练时间

# 4. 比赛训练建议（150~200字）
请说明：
- 如何进行 Codeforces 虚拟赛
- 赛后如何复盘
- 平时如何把补题和专题训练结合起来

补充要求：
- 输出尽量简洁，但要保证信息完整
- 建议必须具体、可执行
- 不要只给空泛鼓励
- 如果数据不足，请明确指出
`, inputJSON)

	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		return "", "", modelName, fmt.Errorf("DEEPSEEK_API_KEY is not set")
	}

	reqBody := dsReq{
		Model: modelName,
		Messages: []dsMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Stream:    false,
		MaxTokens: 2000,
	}
	j, _ := json.Marshal(reqBody)

	httpReq, _ := http.NewRequestWithContext(ctx, "POST", "https://api.deepseek.com/chat/completions", bytes.NewReader(j))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 120 * time.Second}

	resp, e := client.Do(httpReq)
	if e != nil {
		return "", "", modelName, fmt.Errorf("deepseek request failed: %w", e)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return "", "", modelName, fmt.Errorf("deepseek read body failed: %w (status=%s)", readErr, resp.Status)
	}
	if len(body) == 0 {
		return "", "", modelName, fmt.Errorf("deepseek empty body (status=%s)", resp.Status)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 把前 500 字符带上，避免太长
		s := string(body)
		if len(s) > 500 {
			s = s[:500]
		}
		return "", "", modelName, fmt.Errorf("deepseek http error (status=%s) body=%s", resp.Status, s)
	}

	var out dsResp
	if e := json.Unmarshal(body, &out); e != nil {
		s := string(body)
		if len(s) > 500 {
			s = s[:500]
		}
		return "", "", modelName, fmt.Errorf("deepseek decode error: %v (status=%s) body=%s", e, resp.Status, s)
	}
	if out.Error != nil {
		return "", "", modelName, fmt.Errorf("deepseek error: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", "", modelName, fmt.Errorf("deepseek empty choices (status=%s) body=%s", resp.Status, string(body))
	}

	reportMD = out.Choices[0].Message.Content
	return reportMD, inputJSON, modelName, nil
}

func dailyRankHandler(c *gin.Context, pool *pgxpool.Pool) {
	rows, err := pool.Query(c, `
		SELECT
			m.id,
			m.name,
			COUNT(DISTINCT s.platform || ':' || s.problem_id) AS solved_count
		FROM submissions s
		JOIN accounts a ON s.account_id = a.id
		JOIN members m ON a.member_id = m.id
		WHERE s.verdict = 'OK'
		  AND s.submitted_at >= CURRENT_DATE
		  AND s.submitted_at < CURRENT_DATE + INTERVAL '1 day'
		GROUP BY m.id, m.name
		ORDER BY solved_count DESC, m.id ASC
	`)
	if err != nil {
		c.String(500, err.Error())
		return
	}
	defer rows.Close()

	type Item struct {
		Rank        int
		MemberID    int64
		Name        string
		SolvedCount int
	}

	var items []Item
	rank := 1

	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.MemberID, &it.Name, &it.SolvedCount); err != nil {
			c.String(500, err.Error())
			return
		}
		it.Rank = rank
		rank++
		items = append(items, it)
	}

	c.HTML(200, "daily_rank.html", gin.H{
		"Items": items,
	})
}
