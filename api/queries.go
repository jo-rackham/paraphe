package main

import (
	"net/http"

	"github.com/jackc/pgx/v5"
)

// Row-shape helpers over the request's transaction. Every one goes through
// s.tx(r), never s.pool: the transaction is what carries the request's scope.

func (s *Server) rows(r *http.Request, sql string, args ...any) ([]map[string]any, error) {
	res, err := s.tx(r).Query(r.Context(), sql, args...)
	if err != nil {
		return nil, err
	}
	rows, err := pgx.CollectRows(res, pgx.RowToMap)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	return rows, nil
}

func (s *Server) column(r *http.Request, sql string, args ...any) ([]string, error) {
	rows, err := s.tx(r).Query(r.Context(), sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v *string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		if v != nil {
			out = append(out, *v)
		}
	}
	return out, rows.Err()
}

func (s *Server) counters(r *http.Request, sql string, args ...any) (map[string]int, error) {
	rows, err := s.tx(r).Query(r.Context(), sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var key *string
		var n int
		if err := rows.Scan(&key, &n); err != nil {
			return nil, err
		}
		if key != nil {
			out[*key] = n
		}
	}
	return out, rows.Err()
}

// orderedCounters keeps the SQL order, which a Go map would lose.
type counter struct {
	Key string `json:"key"`
	N   int    `json:"n"`
}

func (s *Server) orderedCounters(r *http.Request, sql string, args ...any) ([]counter, error) {
	rows, err := s.tx(r).Query(r.Context(), sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []counter{}
	for rows.Next() {
		var key *string
		var n int
		if err := rows.Scan(&key, &n); err != nil {
			return nil, err
		}
		if key != nil {
			out = append(out, counter{*key, n})
		}
	}
	return out, rows.Err()
}
