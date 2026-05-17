package controller

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/goccy/go-json"
	"github.com/gorilla/websocket"
	"github.com/hashicorp/go-uuid"
	"golang.org/x/sync/singleflight"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/utils"
	"github.com/nezhahq/nezha/service/singleton"
)

var upgrader *websocket.Upgrader

func InitUpgrader() {
	var checkOrigin func(r *http.Request) bool

	// Allow CORS from loopback addresses in debug mode
	if singleton.Conf.Debug {
		checkOrigin = func(r *http.Request) bool {
			if checkSameOrigin(r) {
				return true
			}
			hostAddr := r.Host
			host, _, err := net.SplitHostPort(hostAddr)
			if err != nil {
				return false
			}
			if ip := net.ParseIP(host); ip != nil {
				if ip.IsLoopback() {
					return true
				}
			} else {
				// Handle domains like "localhost"
				ip, err := net.LookupHost(host)
				if err != nil || len(ip) == 0 {
					return false
				}
				if netIP := net.ParseIP(ip[0]); netIP != nil && netIP.IsLoopback() {
					return true
				}
			}
			return false
		}
	}

	upgrader = &websocket.Upgrader{
		ReadBufferSize:  32768,
		WriteBufferSize: 32768,
		CheckOrigin:     checkOrigin,
	}
}

func equalASCIIFold(s, t string) bool {
	for s != "" && t != "" {
		sr, size := utf8.DecodeRuneInString(s)
		s = s[size:]
		tr, size := utf8.DecodeRuneInString(t)
		t = t[size:]
		if sr == tr {
			continue
		}
		if 'A' <= sr && sr <= 'Z' {
			sr = sr + 'a' - 'A'
		}
		if 'A' <= tr && tr <= 'Z' {
			tr = tr + 'a' - 'A'
		}
		if sr != tr {
			return false
		}
	}
	return s == t
}

func checkSameOrigin(r *http.Request) bool {
	origin := r.Header["Origin"]
	if len(origin) == 0 {
		return true
	}
	u, err := url.Parse(origin[0])
	if err != nil {
		return false
	}
	return equalASCIIFold(u.Host, r.Host)
}

// Websocket server stream
// @Summary Websocket server stream
// @tags common
// @Schemes
// @Description Websocket server stream
// @security BearerAuth
// @Produce json
// @Success 200 {object} model.StreamServerData
// @Router /ws/server [get]
func serverStream(c *gin.Context) (any, error) {
	connId, err := uuid.GenerateUUID()
	if err != nil {
		return nil, newWsError("%v", err)
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return nil, newWsError("%v", err)
	}
	defer conn.Close()

	userIp := c.GetString(model.CtxKeyRealIPStr)
	if userIp == "" {
		userIp = c.RemoteIP()
	}

	u, isMember := c.Get(model.CtxKeyAuthorizedUser)
	var (
		userId  uint64
		isAdmin bool
	)
	if isMember {
		user := u.(*model.User)
		userId = user.ID
		isAdmin = user.Role.IsAdmin()
	}

	singleton.AddOnlineUser(connId, &model.OnlineUser{
		UserID:      userId,
		IP:          userIp,
		ConnectedAt: time.Now(),
		Conn:        conn,
	})
	defer singleton.RemoveOnlineUser(connId)

	count := 0
	for {
		stat, err := getServerStat(count == 0, userId, isAdmin)
		if err != nil {
			continue
		}
		if err := conn.WriteMessage(websocket.TextMessage, stat); err != nil {
			break
		}
		count += 1
		if count%4 == 0 {
			err = conn.WriteMessage(websocket.PingMessage, []byte{})
			if err != nil {
				break
			}
		}
		time.Sleep(time.Second * 2)
	}
	return nil, newWsError("")
}

var requestGroup singleflight.Group

// getServerStat returns the websocket frame the viewer is allowed to see.
// The cache key must include the viewer's identity because the projection
// depends on per-server ownership: prior to GHSA-hvv7-hfrh-7gxj this function
// used a single isMember flag and leaked HideForGuest servers plus full Host
// (PlatformVersion, agent Version, GPU) to every authenticated user.
func getServerStat(withPublicNote bool, viewerUserID uint64, viewerIsAdmin bool) ([]byte, error) {
	cacheKey := fmt.Sprintf("serverStats::%t::%t::%d", withPublicNote, viewerIsAdmin, viewerUserID)
	v, err, _ := requestGroup.Do(cacheKey, func() (any, error) {
		servers := filterServersForViewer(
			singleton.ServerShared.GetSortedList(),
			viewerUserID, viewerIsAdmin, withPublicNote,
		)
		return json.Marshal(model.StreamServerData{
			Now:     time.Now().Unix() * 1000,
			Online:  singleton.GetOnlineUserCount(),
			Servers: servers,
		})
	})

	return v.([]byte), err
}

// filterServersForViewer projects the global server list down to what a single
// viewer is allowed to see. The rules are:
//   - HideForGuest servers are visible only to their owner and to admins.
//   - Non-owner / non-admin viewers (including authenticated members) get
//     Host.Filter() output, which drops PlatformVersion and agent Version.
//   - Admins are unconstrained.
//
// viewerUserID == 0 represents an unauthenticated guest.
func filterServersForViewer(servers []*model.Server, viewerUserID uint64, viewerIsAdmin bool, withPublicNote bool) []model.StreamServer {
	out := make([]model.StreamServer, 0, len(servers))
	for _, server := range servers {
		isOwnerOrAdmin := viewerIsAdmin || (viewerUserID != 0 && server.UserID == viewerUserID)
		if server.HideForGuest && !isOwnerOrAdmin {
			continue
		}
		var countryCode string
		if server.GeoIP != nil {
			countryCode = server.GeoIP.CountryCode
		}
		out = append(out, model.StreamServer{
			ID:           server.ID,
			Name:         server.Name,
			PublicNote:   utils.IfOr(withPublicNote, server.PublicNote, ""),
			DisplayIndex: server.DisplayIndex,
			Host:         utils.IfOr(isOwnerOrAdmin, server.Host, server.Host.Filter()),
			State:        server.State,
			CountryCode:  countryCode,
			LastActive:   server.LastActive,
		})
	}
	return out
}
