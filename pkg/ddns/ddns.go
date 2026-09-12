package ddns

import (
	"context"
	"fmt"
	"log"
	"net/netip"
	"strings"
	"time"

	"github.com/libdns/libdns"
	"github.com/miekg/dns"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/utils"
)

type DNSServerKey struct{}

const (
	dnsTimeOut = 10 * time.Second
)

type Provider struct {
	prefix string
	zone   string

	DDNSProfile *model.DDNSProfile
	IPAddrs     *model.IP
	Setter      libdns.RecordSetter
}

func (provider *Provider) GetProfileID() uint64 {
	return provider.DDNSProfile.ID
}

func (provider *Provider) UpdateDomain(ctx context.Context, overrideDomains ...string) {
	for _, domain := range utils.IfOr(len(overrideDomains) > 0, overrideDomains, provider.DDNSProfile.Domains) {
		for retries := range int(provider.DDNSProfile.MaxRetries) {
			log.Printf("NEZHA>> Updating DNS Record of domain %s: %d/%d", domain, retries+1, provider.DDNSProfile.MaxRetries)
			if err := provider.updateDomain(ctx, domain); err != nil {
				log.Printf("NEZHA>> Failed to update DNS record of domain %s: %v", domain, err)
			} else {
				log.Printf("NEZHA>> Update DNS record of domain %s succeeded", domain)
				break
			}
		}
	}
}

func (provider *Provider) updateDomain(ctx context.Context, domain string) error {
	var err error
	provider.prefix, provider.zone, err = provider.splitDomainSOA(ctx, domain)
	if err != nil {
		return err
	}

	// 独立处理 IPv4 更新或删除
	if *provider.DDNSProfile.EnableIPv4 {
		if provider.IPAddrs.IPv4Addr == "" {
			log.Printf("NEZHA>> IPv4 address is empty for domain %s, deleting A record...", domain)
			if err = provider.deleteDomainRecord(ctx, "A"); err != nil {
				return fmt.Errorf("failed to delete IPv4 record: %w", err)
			}
		} else {
			if err = provider.addDomainRecord(ctx, "A", provider.IPAddrs.IPv4Addr); err != nil {
				return err
			}
		}
	}

	// 独立处理 IPv6 更新或删除
	if *provider.DDNSProfile.EnableIPv6 {
		if provider.IPAddrs.IPv6Addr == "" {
			log.Printf("NEZHA>> IPv6 address is empty for domain %s, deleting AAAA record...", domain)
			if err = provider.deleteDomainRecord(ctx, "AAAA"); err != nil {
				return fmt.Errorf("failed to delete IPv6 record: %w", err)
			}
		} else {
			if err = provider.addDomainRecord(ctx, "AAAA", provider.IPAddrs.IPv6Addr); err != nil {
				return err
			}
		}
	}

	return nil
}

func (provider *Provider) addDomainRecord(ctx context.Context, recType, addr string) error {
	netipAddr, err := netip.ParseAddr(addr)
	if err != nil {
		return fmt.Errorf("parse error: %v", err)
	}

	_, err = provider.Setter.SetRecords(ctx, provider.zone,
		[]libdns.Record{
			libdns.Address{
				Name: provider.prefix,
				IP:   netipAddr,
				TTL:  time.Minute,
			},
		})
	return err
}

// deleteDomainRecord 用于删除指定类型的解析记录
func (provider *Provider) deleteDomainRecord(ctx context.Context, recType string) error {
	// 检查 libdns.RecordGetter 和 libdns.RecordDeleter
	getter, ok := provider.Setter.(libdns.RecordGetter)
	if !ok {
		return fmt.Errorf("dns provider does not support record getting")
	}

	deleter, ok := provider.Setter.(libdns.RecordDeleter)
	if !ok {
		return fmt.Errorf("dns provider does not support record deletion")
	}

	// 获取 DNS 记录
	allRecords, err := getter.GetRecords(ctx, provider.zone)
	if err != nil {
		return fmt.Errorf("failed to get DNS records: %w", err)
	}

	cleanName := func(name string) string {
		return strings.ToLower(strings.TrimSuffix(name, "."))
	}

	cleanPrefix := cleanName(provider.prefix)
	cleanZone := cleanName(provider.zone)
	targetRecType := strings.ToUpper(recType)

	// 筛选和匹配
	var targetRecords []libdns.Record
	for _, rec := range allRecords {
		rr := rec.RR()
		recName := rr.Name
		currentType := strings.ToUpper(rr.Type)

		if currentType != targetRecType {
			continue
		}

		relName := libdns.RelativeName(recName, provider.zone)
		cleanRel := cleanName(relName)
		cleanRRName := cleanName(recName)

		var matchedName bool
		if cleanPrefix == "" {
			matchedName = cleanRel == "" || cleanRel == "@" || cleanRRName == cleanZone
		} else {
			matchedName = cleanRel == cleanPrefix || cleanRRName == cleanPrefix
		}

		// 保留原始的 rec 接口（包含 Cloudflare 所需的 ProviderData 元数据），用于精准删除
		if matchedName {
			targetRecords = append(targetRecords, rec)
		}
	}

	// 若未找到对应记录，则直接返回成功（视为已删除或不存在）
	if len(targetRecords) == 0 {
		log.Printf("NEZHA>> No matching %s record found for deletion under zone %s", recType, provider.zone)
		return nil
	}

	// 执行删除操作
	_, err = deleter.DeleteRecords(ctx, provider.zone, targetRecords)
	if err != nil {
		return fmt.Errorf("deleter.DeleteRecords failed: %w", err)
	}

	log.Printf("NEZHA>> Successfully deleted %d matching %s record(s) for %s.%s", len(targetRecords), recType, provider.prefix, provider.zone)
	return nil
}

func (provider *Provider) splitDomainSOA(ctx context.Context, domain string) (prefix string, zone string, err error) {
	c := &dns.Client{Timeout: dnsTimeOut}

	domain += "."
	indexes := dns.Split(domain)

	servers := utils.DNSServers

	customDNSServers, _ := ctx.Value(DNSServerKey{}).([]string)
	if len(customDNSServers) > 0 {
		servers = customDNSServers
	}

	for _, server := range servers {
		for _, idx := range indexes {
			var m dns.Msg
			m.SetQuestion(domain[idx:], dns.TypeSOA)

			r, _, err := c.Exchange(&m, server)
			if err != nil {
				continue
			}

			if len(r.Answer) > 0 {
				if soa, ok := r.Answer[0].(*dns.SOA); ok {
					zone := soa.Hdr.Name
					prefix := libdns.RelativeName(domain, zone)
					// Convert "@" to empty string for zone apex
					if prefix == "@" {
						prefix = ""
					}
					return prefix, zone, nil
				}
			}
		}
	}

	return "", "", fmt.Errorf("SOA record not found for domain: %s", domain)
}
