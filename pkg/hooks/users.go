package hooks

import (
	"github.com/canonical/hook-service/internal/logging"
	"github.com/ory/hydra/v2/oauth2"
)

type User struct {
	SubjectId string
	ClientId  string
	Email     string
}

func (u *User) GetUserId() string {
	if u.SubjectId != "" {
		return u.SubjectId
	}
	if u.ClientId != "" {
		return u.ClientId
	}
	return ""
}

func NewUserFromHookRequest(r *oauth2.TokenHookRequest, logger logging.LoggerInterface) *User {
	u := new(User)
	if isServiceAccount(r.Request.GrantTypes) {
		u.ClientId = r.Request.ClientID
		return u
	}

	u.SubjectId = r.Session.Subject
	s := r.Session.DefaultSession
	if s != nil && s.Claims != nil {
		email, ok := s.Claims.Extra["email"].(string)
		if !ok {
			logger.Warnf("Failed to extract the user: %#v", u)
			return u
		}
		u.Email = email
	}
	return u
}

func isServiceAccount(grantTypes []string) bool {
	return exactOne(grantTypes, GrantTypeClientCredentials) ||
		exactOne(grantTypes, GrantTypeJWTBearer)
}

func exactOne(vs []string, v string) bool {
	return len(vs) == 1 && vs[0] == v
}
