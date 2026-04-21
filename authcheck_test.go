package main

import "testing"

func TestFilterAuthHeadersMatchesNewestSubject(t *testing.T) {
	headers := []string{
		"Subject: old\r\nMessage-ID: <1>\r\n\r\n",
		"Subject: wanted\r\nMessage-ID: <2>\r\n\r\n",
		"Subject: wanted\r\nMessage-ID: <3>\r\n\r\n",
	}
	got := filterAuthHeaders(headers, "wanted", 1)
	if len(got) != 1 {
		t.Fatalf("expected 1 header, got %d", len(got))
	}
	if got[0] != headers[2] {
		t.Fatalf("expected newest matching header, got %q", got[0])
	}
}

func TestFilterAuthHeadersSkipsMalformedAndNonMatching(t *testing.T) {
	headers := []string{
		"not a header block",
		"Subject: other\r\nMessage-ID: <1>\r\n\r\n",
		"Subject: wanted\r\nMessage-ID: <2>\r\n\r\n",
	}
	got := filterAuthHeaders(headers, "wanted", 2)
	if len(got) != 1 {
		t.Fatalf("expected 1 header, got %d", len(got))
	}
	if got[0] != headers[2] {
		t.Fatalf("expected matching header, got %q", got[0])
	}
}
