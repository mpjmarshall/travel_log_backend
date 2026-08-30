package admin

import (
	"fmt"
	"strconv"

	"travellog/internal/postgres"
)

const dayFormat = "2 Jan 2006"

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// plural writes a count with its noun, adding the s only when it is needed.
func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// humanBytes is for reading, never for arithmetic.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

func sessionRows(in []postgres.SessionRow) []SessionRow {
	out := make([]SessionRow, 0, len(in))
	for _, s := range in {
		out = append(out, SessionRow{
			ID:         s.ID,
			Email:      s.Email,
			CreatedAt:  s.CreatedAt.Format(dayFormat),
			LastUsedAt: s.LastUsedAt.Format(dayFormat),
		})
	}
	return out
}

func inviteRows(in []postgres.InviteRow) []InviteRow {
	out := make([]InviteRow, 0, len(in))
	for _, i := range in {
		out = append(out, InviteRow{
			Hash:      fmt.Sprintf("%x", i.Hash),
			Note:      i.Note,
			CreatedAt: i.CreatedAt.Format(dayFormat),
			Used:      i.Used,
			UsedBy:    i.UsedBy,
		})
	}
	return out
}
