package main

import "testing"

func TestFilterHeaderSeqNumsMatchesNewestMessageID(t *testing.T) {
	headers := []recentHeader{
		{SeqNum: 10, Header: "Subject: A\r\nMessage-ID: <1>\r\n\r\n"},
		{SeqNum: 11, Header: "Subject: B\r\nMessage-ID: <2>\r\n\r\n"},
		{SeqNum: 12, Header: "Subject: B\r\nMessage-ID: <3>\r\n\r\n"},
	}
	got := filterHeaderSeqNums(headers, "B", "", 1)
	if len(got) != 1 || got[0] != 12 {
		t.Fatalf("unexpected seqs: %#v", got)
	}
}

func TestFilterHeaderSeqNumsMatchesExactMessageID(t *testing.T) {
	headers := []recentHeader{
		{SeqNum: 21, Header: "Subject: A\r\nMessage-ID: <one>\r\n\r\n"},
		{SeqNum: 22, Header: "Subject: A\r\nMessage-ID: <two>\r\n\r\n"},
	}
	got := filterHeaderSeqNums(headers, "", "<one>", 1)
	if len(got) != 1 || got[0] != 21 {
		t.Fatalf("unexpected seqs: %#v", got)
	}
}
