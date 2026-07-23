package operations

import (
	"errors"
	"testing"
	"time"
)

func TestValidateAnnouncement(t *testing.T) {
	now := time.Now()
	before := now.Add(-time.Minute)
	tests := []struct {
		name  string
		input AnnouncementInput
		valid bool
	}{
		{name: "valid", input: AnnouncementInput{Title: "维护通知", Body: "今晚维护"}, valid: true},
		{name: "missing title", input: AnnouncementInput{Body: "内容"}},
		{name: "missing body", input: AnnouncementInput{Title: "标题"}},
		{name: "invalid window", input: AnnouncementInput{Title: "标题", Body: "内容", StartsAt: &now, EndsAt: &before}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateAnnouncement(test.input)
			if test.valid && err != nil {
				t.Fatalf("validate: %v", err)
			}
			if !test.valid && !errors.Is(err, ErrInvalidAnnouncement) {
				t.Fatalf("error = %v, want ErrInvalidAnnouncement", err)
			}
		})
	}
}
