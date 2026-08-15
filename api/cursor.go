package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// cursor: the last row of a page, in the sort's own order. Opaque to the
// client — it is a position in a result set, not something to compose.
type cursor struct {
	Score      int
	Department string
	Commune    string
	Insee      string
}

func encodeCursor(score, department, commune, insee string) string {
	n, _ := strconv.Atoi(strings.TrimSpace(score)) // unreadable sorts as 0, like the query
	raw, _ := json.Marshal(cursor{Score: -n, Department: department,
		Commune: commune, Insee: insee})
	return base64.RawURLEncoding.EncodeToString(raw)
}

// A cursor that does not decode is REFUSED, not silently treated as "start
// from the beginning": serving page 1 in its place returns page 1's own
// cursor, so the browser asks again, forever.
//
// JSON rather than a separator: a commune is free to contain any byte, and
// a delimiter that data can carry is a delimiter that will be carried.
func decodeCursor(raw string) (*cursor, error) {
	if raw == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("cursor is not base64: %w", err)
	}
	var c cursor
	dec := json.NewDecoder(strings.NewReader(string(decoded)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("cursor is not a position: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("cursor carries more than one position")
	}
	// pgx encodes it as int4: outside that range the query raises, and the
	// volunteer reads "erreur interne" for a bookmark
	if c.Score < math.MinInt32 || c.Score > math.MaxInt32 {
		return nil, fmt.Errorf("cursor score %d is out of range", c.Score)
	}
	// same refusal as the NUL stripped from `q`: PostgreSQL rejects the
	// byte in any text value, and these three travel the same query
	if strings.ContainsRune(c.Department+c.Commune+c.Insee, 0) {
		return nil, fmt.Errorf("cursor carries a NUL byte")
	}
	return &c, nil
}
