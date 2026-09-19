package parser

import "testing"

func TestExtractApplicationDataEmail(t *testing.T) {
	text := "Interested candidates should send CV to Recruitment@Example.CO.ID with subject Sales Executive."
	got := ExtractApplicationData(text)
	if got.Method != ApplicationMethodEmail {
		t.Fatalf("method=%q", got.Method)
	}
	if got.PrimaryEmail != "recruitment@example.co.id" {
		t.Fatalf("email=%q", got.PrimaryEmail)
	}
	if len(got.Emails) != 1 {
		t.Fatalf("emails=%v", got.Emails)
	}
	if got.Instruction == "" {
		t.Fatal("expected instruction")
	}
}

func TestExtractApplicationDataMultipleEmailsChoosesApplicationContext(t *testing.T) {
	text := "General info: info@example.com. To apply, please send your CV to career@example.com."
	got := ExtractApplicationData(text)
	if got.PrimaryEmail != "career@example.com" {
		t.Fatalf("primary=%q emails=%v", got.PrimaryEmail, got.Emails)
	}
}

func TestExtractApplicationDataExternalURL(t *testing.T) {
	text := "To apply for this role, submit your application at https://careers.example.com/jobs/123."
	got := ExtractApplicationData(text)
	if got.Method != ApplicationMethodExternalURL {
		t.Fatalf("method=%q", got.Method)
	}
	if got.ApplyURL != "https://careers.example.com/jobs/123" {
		t.Fatalf("url=%q", got.ApplyURL)
	}
}

func TestExtractApplicationDataDoesNotTreatHomepageAsApplyURL(t *testing.T) {
	text := "Learn more about our company at https://example.com/about and join our growing team."
	got := ExtractApplicationData(text)
	if got.Method != ApplicationMethodUnknown {
		t.Fatalf("method=%q url=%q", got.Method, got.ApplyURL)
	}
}

func TestExtractApplicationDataIgnoresLinkedInURL(t *testing.T) {
	text := "Apply here: https://www.linkedin.com/jobs/view/123/"
	got := ExtractApplicationData(text)
	if got.Method != ApplicationMethodUnknown {
		t.Fatalf("method=%q url=%q", got.Method, got.ApplyURL)
	}
}
