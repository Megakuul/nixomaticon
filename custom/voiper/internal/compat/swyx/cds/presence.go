package cds

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

// PresenceConfiguration is the user's own away/DND state and status text.
// A PUT replaces the whole configuration, so callers read it first and change
// only their fields. Dates use the server's WCF encoding and pass through raw.
type PresenceConfiguration struct {
	IsAway                              bool
	DoNotDisturb                        bool
	AwayText                            string
	Expiration                          json.RawMessage `json:",omitempty"`
	AppointmentText                     string
	AppointmentExpiration               json.RawMessage `json:",omitempty"`
	LoginDeviceType                     int
	ShowMeAwayFromFederatedAppointments bool
	ShowFederatedAppointmentAsAwayText  bool
}

type Presence struct {
	Status              int
	StatusText          string
	IsForwardingEnabled bool
}

type EmployeePresence struct {
	SiteId, EmployeeId int
	Presence           Presence
}

type EmployeeNumber struct {
	Number     string
	NumberType int
}

const InternalNumber = 1

type Employee struct {
	SiteId, EmployeeId int
	Name               string
	Number             string
	ChatUserId         string
	Presence           Presence
	AllEmployeeNumbers []EmployeeNumber
}

type User struct {
	UserId          int
	Name            string
	MessagingSystem int
}

// userPath logs in first when needed, because the user ID comes from the login.
func (c *Client) userPath(ctx context.Context, suffix string) (string, error) {
	if c.UserID() == 0 {
		if err := c.Login(ctx); err != nil {
			return "", err
		}
	}
	return "/user/" + strconv.Itoa(c.UserID()) + suffix, nil
}

func (c *Client) User(ctx context.Context) (User, error) {
	var user User
	path, err := c.userPath(ctx, "")
	if err == nil {
		err = c.call(ctx, http.MethodGet, path, nil, &user)
	}
	return user, err
}

func (c *Client) PresenceConfiguration(ctx context.Context) (PresenceConfiguration, error) {
	var cfg PresenceConfiguration
	path, err := c.userPath(ctx, "/presenceconfiguration")
	if err == nil {
		err = c.call(ctx, http.MethodGet, path, nil, &cfg)
	}
	return cfg, err
}

func (c *Client) SetPresenceConfiguration(ctx context.Context, cfg PresenceConfiguration) error {
	path, err := c.userPath(ctx, "/presenceconfiguration")
	if err != nil {
		return err
	}
	return c.call(ctx, http.MethodPut, path, cfg, nil)
}

// Employees returns one page of the colleagues' directory, including their
// numbers and current status.
func (c *Client) Employees(ctx context.Context, index, count int) ([]Employee, error) {
	var employees []Employee
	query := url.Values{"paginationIndex": {strconv.Itoa(index)}, "paginationCount": {strconv.Itoa(count)}}
	err := c.call(ctx, http.MethodGet, "/employees/?"+query.Encode(), nil, &employees)
	return employees, err
}

// PresenceChanges returns the status of colleagues changed after since (0 for
// all) and the server timestamp to pass on the next call.
func (c *Client) PresenceChanges(ctx context.Context, since int64) ([]EmployeePresence, int64, error) {
	var list struct {
		Timstamp int64 // sic, the server's spelling
		List     []EmployeePresence
	}
	path := "/employees/presence"
	if since != 0 {
		path += "?clientTimestamp=" + strconv.FormatInt(since, 10)
	}
	err := c.call(ctx, http.MethodGet, path, nil, &list)
	return list.List, list.Timstamp, err
}
