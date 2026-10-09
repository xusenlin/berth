package logs

import "testing"

func TestParse(t *testing.T) {
	e, ok := Parse(`{"__REALTIME_TIMESTAMP":"1760000000123456","PRIORITY":"3","_PID":"42","MESSAGE":"\u001b[31mboom\u001b[0m","_TRANSPORT":"stdout"}`)
	if !ok || e.Time != 1760000000123 || e.Priority != 3 || e.PID != 42 || e.Message != "\x1b[31mboom\x1b[0m" || e.Source != "stdout" {
		t.Fatalf("entry = %+v", e)
	}
	e, ok = Parse(`{"__REALTIME_TIMESTAMP":"1000","MESSAGE":[104,105,255],"_COMM":"systemd"}`)
	if !ok || e.Message != "hi�" || e.Priority != 6 || e.Source != "systemd" {
		t.Fatalf("bytes entry = %+v", e)
	}
	if _, ok := Parse("-- No entries --"); ok {
		t.Error("non-JSON parsed")
	}
}
