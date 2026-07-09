package models

// Profile is a researcher/scientist identity used to group run history. Auth is
// intentionally minimal — a profile is created from a short form with no
// validation or verification; it just anchors runs to a person so someone can
// track their own history. Optional fields default to "" and are omitted from
// JSON when empty.
type Profile struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Email       string `json:"email,omitempty"`
	Institution string `json:"institution,omitempty"`
	Field       string `json:"field,omitempty"` // research area, e.g. "oncology"
	ORCID       string `json:"orcid,omitempty"`
	CreatedAt   string `json:"created_at"`
}
