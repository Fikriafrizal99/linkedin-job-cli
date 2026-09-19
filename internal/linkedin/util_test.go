package linkedin

import "testing"

func TestCleanHTMLTextDecodesNestedEntities(t *testing.T) {
	got := cleanHTMLText("Selling &amp;amp; Coordinating &amp; Reporting")
	want := "Selling & Coordinating & Reporting"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestCleanHTMLTextStripsMarkupAfterDecode(t *testing.T) {
	got := cleanHTMLText("&lt;p&gt;Send CV &amp;amp; portfolio&lt;/p&gt;")
	want := "Send CV & portfolio"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
