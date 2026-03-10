package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:5432/acmcoach?sslmode=disable"
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		panic(err)
	}
	defer pool.Close()

	// 取所有 member_id
	rows, err := pool.Query(ctx, `SELECT id FROM members ORDER BY id`)
	if err != nil {
		panic(err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			panic(err)
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		fmt.Println("no members found")
		return
	}

	asOf := time.Now().UTC()

	for _, memberID := range ids {
		if err := upsertStatsForMember(ctx, pool, memberID, asOf); err != nil {
			fmt.Printf("[member %d] agg error: %v\n", memberID, err)
			continue
		}
		fmt.Printf("[member %d] agg ok\n", memberID)
	}

	fmt.Println("done")
}

func upsertStatsForMember(ctx context.Context, pool *pgxpool.Pool, memberID int64, asOf time.Time) error {
	// 用 SQL 一次性算 7/30/120 并 upsert
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
