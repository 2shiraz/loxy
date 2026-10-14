package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/2shiraz/loxy/internal/api/gen"
	"github.com/2shiraz/loxy/internal/auth"
	"github.com/2shiraz/loxy/internal/store/db"
)

func totpPendingKey(uid int64) string { return fmt.Sprintf("totp_pending_%d", uid) }

// TotpSetup creates a pending secret; it becomes active after TotpEnable confirms a code.
func (s *Server) TotpSetup(w http.ResponseWriter, r *http.Request) {
	p := userFrom(r)
	if p.TokenID != 0 {
		fail(w, &apiError{Status: 403, Code: "forbidden", Msg: "not available for API tokens"})
		return
	}
	secret := auth.NewTOTPSecret()
	enc, _ := json.Marshal(s.Box.Seal([]byte(secret)))
	if err := s.Store.Q.PutSetting(r.Context(), db.PutSettingParams{Key: totpPendingKey(p.User.ID), Value: string(enc)}); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"secret": secret, "otpauth_url": auth.TOTPURL(secret, p.User.Email)})
}

func (s *Server) TotpEnable(w http.ResponseWriter, r *http.Request) {
	p := userFrom(r)
	var in gen.TotpEnableJSONBody
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	v, err := s.Store.Q.GetSetting(r.Context(), totpPendingKey(p.User.ID))
	var enc []byte
	if err != nil || json.Unmarshal([]byte(v), &enc) != nil {
		fail(w, errField("code", "start the setup first"))
		return
	}
	secret, err := s.Box.Open(enc)
	if err != nil || !auth.CheckTOTP(string(secret), in.Code, time.Now()) {
		fail(w, errField("code", "invalid code"))
		return
	}
	if err := s.Store.Q.SetUserTOTP(r.Context(), db.SetUserTOTPParams{TotpSecretEnc: enc, ID: p.User.ID}); err != nil {
		fail(w, err)
		return
	}
	_, _ = s.Store.DB.ExecContext(r.Context(), `DELETE FROM settings WHERE key = ?`, totpPendingKey(p.User.ID))
	s.audit(r, "auth.totp.enable", "user", p.User.ID, nil)
	w.WriteHeader(204)
}

func (s *Server) TotpDisable(w http.ResponseWriter, r *http.Request) {
	p := userFrom(r)
	var in gen.TotpDisableJSONBody
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if !s.totpOK(p.User, in.Code) {
		fail(w, errField("code", "invalid code"))
		return
	}
	if err := s.Store.Q.SetUserTOTP(r.Context(), db.SetUserTOTPParams{TotpSecretEnc: nil, ID: p.User.ID}); err != nil {
		fail(w, err)
		return
	}
	s.audit(r, "auth.totp.disable", "user", p.User.ID, nil)
	w.WriteHeader(204)
}

func totpEnabled(u db.User) bool { return len(u.TotpSecretEnc) > 0 }

func (s *Server) totpOK(u db.User, code string) bool {
	if !totpEnabled(u) {
		return false
	}
	secret, err := s.Box.Open(u.TotpSecretEnc)
	return err == nil && auth.CheckTOTP(string(secret), code, time.Now())
}
