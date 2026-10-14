package runtime

import "testing"

func TestCheck(t *testing.T) {
	ok := map[string]string{
		"add server b/s 1.2.3.4:80": "New server registered.\n",
		"enable server b/s":         "\n",
		"set server b/s weight 5":   "",
		"commit ssl cert /x.pem":    "Committing /x.pem.\nSuccess!\n",
		"del server b/s":            "Server deleted.\n",
	}
	for c, o := range ok {
		if err := Check(c, o); err != nil {
			t.Errorf("%s: %v", c, err)
		}
	}
	bad := map[string]string{
		"add server b/s 1.2.3.4:80": "No such backend.\n",
		"enable server b/s":         "No such server.\n",
		"unknown":                   "Unknown command\n",
	}
	for c, o := range bad {
		if err := Check(c, o); err == nil {
			t.Errorf("%s: expected error", c)
		}
	}
}

func TestParseReload(t *testing.T) {
	if !ParseReload("Success=1\n--\n[NOTICE] ...\n").Success {
		t.Error("success")
	}
	if ParseReload("Success=0\n--\n[ALERT] parsing\n").Success {
		t.Error("failure")
	}
}
