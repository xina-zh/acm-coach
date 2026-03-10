package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type CFResp[T any] struct {
	Status  string `json:"status"`
	Comment string `json:"comment"`
	Result  T      `json:"result"`
}

type CFSubmission struct {
	ID                   int64  `json:"id"`
	ContestID             int64  `json:"contestId"`
	CreationTimeSeconds   int64  `json:"creationTimeSeconds"`
	Verdict               string `json:"verdict"`
	ProgrammingLanguage   string `json:"programmingLanguage"`
	Problem               struct {
		Name   string `json:"name"`
		Index  string `json:"index"`
		Rating *int   `json:"rating"`
	} `json:"problem"`
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		// docker-compose 暴露到本机 5432，所以这里用 localhost
		dbURL = "postgres://postgres:postgres@localhost:5432/acmcoach?sslmode=disable"
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		panic(err)
	}
	defer pool.Close()

	// 1) 从数据库读取所有 codeforces 账号
	rows, err := pool.Query(ctx, `
		SELECT id, handle
		FROM accounts
		WHERE platform = 'codeforces'
		ORDER BY id
	`)
	if err != nil {
		panic(err)
	}
	defer rows.Close()

	type account struct {
		ID     int64
		Handle string
	}
	var accts []account
	for rows.Next() {
		var a account
		if err := rows.Scan(&a.ID, &a.Handle); err != nil {
			panic(err)
		}
		accts = append(accts, a)
	}
	if len(accts) == 0 {
		fmt.Println("no codeforces accounts found in DB")
		return
	}

	client := &http.Client{Timeout: 15 * time.Second}

	for _, a := range accts {
		// 2) 查已有最大 submission_id（增量）
		var maxID int64
		_ = pool.QueryRow(ctx, `
			SELECT COALESCE(MAX(submission_id), 0)
			FROM submissions
			WHERE platform='codeforces' AND account_id=$1
		`, a.ID).Scan(&maxID)

		// 3) 拉取 CF user.status
		url := fmt.Sprintf("https://codeforces.com/api/user.status?handle=%s&from=1&count=200", a.Handle)
		req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("[account %d %s] request error: %v\n", a.ID, a.Handle, err)
			continue
		}

		var out CFResp[[]CFSubmission]
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			_ = resp.Body.Close()
			fmt.Printf("[account %d %s] decode error: %v\n", a.ID, a.Handle, err)
			continue
		}
		_ = resp.Body.Close()

		if out.Status != "OK" {
			fmt.Printf("[account %d %s] cf api error: %s\n", a.ID, a.Handle, out.Comment)
			continue
		}

		// 4) 插入新增 submissions
		inserted := 0
		for _, s := range out.Result {
			if s.ID <= maxID {
				continue
			}
			// problem_id：先用 contestId + index（足够唯一）
			problemID := fmt.Sprintf("%d%s", s.ContestID, s.Problem.Index)

			var rating any = nil
			if s.Problem.Rating != nil {
				rating = *s.Problem.Rating
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
				fmt.Printf("[account %d %s] insert error: %v\n", a.ID, a.Handle, err)
				continue
			}
			inserted++
		}

		fmt.Printf("[account %d %s] fetched=%d inserted=%d (max_before=%d)\n",
			a.ID, a.Handle, len(out.Result), inserted, maxID)
	}

	fmt.Println("done")
}
