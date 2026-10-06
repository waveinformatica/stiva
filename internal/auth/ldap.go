package auth

import (
	"context"
	"crypto/tls"
	"fmt"

	"github.com/go-ldap/ldap/v3"
)

// LDAPRealm authenticates against an LDAP / Active Directory server.
type LDAPRealm struct {
	cfg *LDAPConfig
}

func NewLDAPRealm(cfg *LDAPConfig) *LDAPRealm { return &LDAPRealm{cfg: cfg} }

func (r *LDAPRealm) Name() string { return "ldap" }

func (r *LDAPRealm) AuthenticatePassword(ctx context.Context, username, password string) (*User, error) {
	if username == "" || password == "" {
		return nil, ErrNoMatch
	}
	conn, err := r.dial()
	if err != nil {
		return nil, ErrNoMatch
	}
	defer conn.Close()

	if r.cfg.BindDN != "" && r.cfg.BindPassword != "" {
		if err := conn.Bind(r.cfg.BindDN, r.cfg.BindPassword); err != nil {
			return nil, ErrNoMatch
		}
	}

	filter := fmt.Sprintf(r.cfg.UserFilter, ldap.EscapeFilter(username))
	nameAttr := r.cfg.UserNameAttr
	if nameAttr == "" {
		nameAttr = "cn"
	}
	search := ldap.NewSearchRequest(
		r.cfg.UserBaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases,
		0, 0, false, filter, []string{"dn", nameAttr}, nil,
	)
	res, err := conn.Search(search)
	if err != nil || len(res.Entries) != 1 {
		return nil, ErrNoMatch
	}
	dn := res.Entries[0].DN
	displayName := res.Entries[0].GetAttributeValue(nameAttr)
	if displayName == "" {
		displayName = username
	}
	if err := conn.Bind(dn, password); err != nil {
		return nil, ErrNoMatch
	}

	var groups []string
	if r.cfg.GroupFilter != "" {
		gfilter := fmt.Sprintf(r.cfg.GroupFilter, ldap.EscapeFilter(username))
		gres, gerr := conn.Search(ldap.NewSearchRequest(
			r.cfg.UserBaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases,
			0, 0, false, gfilter, []string{"cn"}, nil))
		if gerr == nil {
			for _, e := range gres.Entries {
				if g := e.GetAttributeValue("cn"); g != "" {
					groups = append(groups, g)
				}
			}
		}
	}
	u := &User{Name: displayName, Groups: groups, Source: "ldap"}
	if r.cfg.AdminGroup != "" {
		for _, g := range groups {
			if g == r.cfg.AdminGroup {
				u.Admin = true
				break
			}
		}
	}
	return u, nil
}

func (r *LDAPRealm) AuthenticateToken(ctx context.Context, token string) (*User, error) {
	return nil, ErrNoMatch
}

func (r *LDAPRealm) dial() (*ldap.Conn, error) {
	tlsCfg := &tls.Config{InsecureSkipVerify: r.cfg.InsecureSkipVerify}
	if r.cfg.StartTLS {
		conn, err := ldap.DialURL(r.cfg.URL)
		if err != nil {
			return nil, err
		}
		if err := conn.StartTLS(tlsCfg); err != nil {
			conn.Close()
			return nil, err
		}
		return conn, nil
	}
	return ldap.DialURL(r.cfg.URL, ldap.DialWithTLSConfig(tlsCfg))
}
