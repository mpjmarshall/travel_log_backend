// The vocabulary the panel's ports speak, declared here so the adapter behind
// them can be replaced and a template renders no database row.
package admin

import "time"

// Overview is the counts the front page carries.
type Overview struct {
	Travellers    int
	Trips         int
	Photos        int
	Places        int
	LiveSessions  int
	UnusedInvites int
	BucketBytes   int64
}

// Traveller is one line of the travellers list.
type Traveller struct {
	ID             string
	Email          string
	Name           string
	CreatedAt      time.Time
	LogbookVersion int64
	Trips          int
	Photos         int
}

// TravellerDetail is that line plus everything the detail page counts. It
// carries no photograph and no URL, by decision.
type TravellerDetail struct {
	Traveller
	Cities      int
	Places      int
	Visits      int
	Walks       int
	BucketBytes int64
}

// Session is one live session, named by its traveller.
type Session struct {
	ID         string
	Email      string
	CreatedAt  time.Time
	LastUsedAt time.Time
	ExpiresAt  time.Time
}

// Invite is one invite, spent or not.
type Invite struct {
	Hash      []byte
	Note      string
	CreatedAt time.Time
	Used      bool
	UsedBy    string
}
