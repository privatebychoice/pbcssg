package appstore

import "testing"

// TestCommentsByIDs: returns the requested comments keyed by id, skips absent ids, empty on nil.
func TestCommentsByIDs(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleMember, "")
	c1, _ := s.AddComment(a.ID, "/p", "", "one")
	c2, _ := s.AddComment(a.ID, "/p", "", "two")

	m, err := s.CommentsByIDs([]int64{c1.ID, c2.ID, 99999})
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 || m[c1.ID].Body != "one" || m[c2.ID].Body != "two" {
		t.Errorf("CommentsByIDs = %+v, want the two comments", m)
	}
	if got, _ := s.CommentsByIDs(nil); len(got) != 0 {
		t.Errorf("nil ids should return empty map, got %v", got)
	}
}
