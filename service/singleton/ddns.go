package singleton

import (
	"cmp"
	"fmt"
	"log"
	"slices"

	"github.com/libdns/cloudflare"
	"github.com/libdns/he"
	tencentcloud "github.com/nezhahq/libdns-tencentcloud"

	"github.com/nezhahq/nezha/model"
	ddns2 "github.com/nezhahq/nezha/pkg/ddns"
	"github.com/nezhahq/nezha/pkg/ddns/dummy"
	"github.com/nezhahq/nezha/pkg/ddns/webhook"
	"github.com/nezhahq/nezha/pkg/utils"
)

type DDNSClass struct {
	class[uint64, *model.DDNSProfile]
}

func NewDDNSClass() *DDNSClass {
	var sortedList []*model.DDNSProfile

	DB.Find(&sortedList)
	list := make(map[uint64]*model.DDNSProfile, len(sortedList))
	for _, profile := range sortedList {
		list[profile.ID] = profile
	}

	dc := &DDNSClass{
		class: class[uint64, *model.DDNSProfile]{
			list:       list,
			sortedList: sortedList,
		},
	}
	return dc
}

func (c *DDNSClass) Update(p *model.DDNSProfile) {
	c.listMu.Lock()
	c.list[p.ID] = p
	c.listMu.Unlock()

	c.sortList()
}

func (c *DDNSClass) Delete(idList []uint64) {
	c.listMu.Lock()
	for _, id := range idList {
		delete(c.list, id)
	}
	c.listMu.Unlock()

	c.sortList()
}

// profileOwnedByRealAdmin reports whether uid is a genuine admin user that
// may share its DDNS profiles globally. userIsAdmin(0) returns true as a
// "system resource" shortcut, but a profile with UserID==0 is a migration /
// default-value artifact, not an admin grant — sharing it with foreign server
// owners reopens GHSA-39g2-8x68-pmx8. A real admin always has a non-zero ID.
func profileOwnedByRealAdmin(uid uint64) bool {
	return uid != 0 && userIsAdmin(uid)
}

// GHSA-39g2-8x68-pmx8: bind-time CheckPermission 对「不存在的 profile ID」放行，
// 攻击者可预绑定将来才会被受害者创建的自增 ID。worker 解析时必须按 ownerUID
// 重新校验归属，跳过非 server owner（且非管理员）所有的 profile。
func (c *DDNSClass) GetDDNSProvidersFromProfiles(profileId []uint64, ip *model.IP, ownerUID uint64) ([]*ddns2.Provider, error) {
	profiles := make([]*model.DDNSProfile, 0, len(profileId))

	c.listMu.RLock()
	for _, id := range profileId {
		if profile, ok := c.list[id]; ok {
			if profile.UserID != ownerUID && !profileOwnedByRealAdmin(profile.UserID) {
				// Fail-closed skip: an admin may bind a member-owned profile,
				// but worker-time only runs same-owner or real-admin profiles.
				log.Printf("NEZHA>> Skipping DDNS profile %d (owner %d) for server owner %d: not owned by server owner or a real admin", profile.ID, profile.UserID, ownerUID)
				continue
			}
			profiles = append(profiles, profile)
		} else {
			c.listMu.RUnlock()
			return nil, fmt.Errorf("cannot find DDNS profile %d", id)
		}
	}
	c.listMu.RUnlock()

	providers := make([]*ddns2.Provider, 0, len(profiles))
	for _, profile := range profiles {
		provider := &ddns2.Provider{DDNSProfile: profile, IPAddrs: ip}
		switch profile.Provider {
		case model.ProviderDummy:
			provider.Setter = &dummy.Provider{}
			providers = append(providers, provider)
		case model.ProviderWebHook:
			provider.Setter = &webhook.Provider{DDNSProfile: profile}
			providers = append(providers, provider)
		case model.ProviderCloudflare:
			provider.Setter = &cloudflare.Provider{APIToken: profile.AccessSecret}
			providers = append(providers, provider)
		case model.ProviderTencentCloud:
			provider.Setter = &tencentcloud.Provider{SecretId: profile.AccessID, SecretKey: profile.AccessSecret}
			providers = append(providers, provider)
		case model.ProviderHE:
			provider.Setter = &he.Provider{APIKey: profile.AccessSecret}
			providers = append(providers, provider)
		default:
			return nil, fmt.Errorf("cannot find DDNS provider %s", profile.Provider)
		}
	}
	return providers, nil
}

func (c *DDNSClass) sortList() {
	c.listMu.RLock()
	defer c.listMu.RUnlock()

	sortedList := utils.MapValuesToSlice(c.list)
	slices.SortFunc(sortedList, func(a, b *model.DDNSProfile) int {
		return cmp.Compare(a.ID, b.ID)
	})

	c.sortedListMu.Lock()
	defer c.sortedListMu.Unlock()
	c.sortedList = sortedList
}
