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
	domains := utils.IfOr(len(overrideDomains) > 0, overrideDomains, provider.DDNSProfile.Domains)
	maxRetries := int(provider.DDNSProfile.MaxRetries)
	if maxRetries <= 0 {
		maxRetries = 1
	}

	for _, domain := range domains {
		var err error
		provider.prefix, provider.zone, err = provider.splitDomainSOA(ctx, domain)
		if err != nil {
			log.Printf("NEZHA>> Failed to split domain SOA for %s: %v", domain, err)
			continue
		}

		// 独立处理 IPv4 更新或删除
		if provider.DDNSProfile.EnableIPv4 != nil && *provider.DDNSProfile.EnableIPv4 {
			for retries := 0; retries < maxRetries; retries++ {
				log.Printf("NEZHA>> Updating IPv4 record of domain %s: %d/%d", domain, retries+1, maxRetries)
				var ipv4Err error
				if provider.IPAddrs.IPv4Addr == "" {
					ipv4Err = provider.deleteDomainRecord(ctx, "A")
				} else {
					ipv4Err = provider.addDomainRecord(ctx, "A", provider.IPAddrs.IPv4Addr)
				}

				if ipv4Err != nil {
					log.Printf("NEZHA>> Failed to update IPv4 record of domain %s: %v", domain, ipv4Err)
				} else {
					log.Printf("NEZHA>> Update IPv4 record of domain %s succeeded", domain)
					break
				}
			}
		}

		// 独立处理 IPv6 更新或删除
		if provider.DDNSProfile.EnableIPv6 != nil && *provider.DDNSProfile.EnableIPv6 {
			for retries := 0; retries < maxRetries; retries++ {
				log.Printf("NEZHA>> Updating IPv6 record of domain %s: %d/%d", domain, retries+1, maxRetries)
				var ipv6Err error
				if provider.IPAddrs.IPv6Addr == "" {
					ipv6Err = provider.deleteDomainRecord(ctx, "AAAA")
				} else {
					ipv6Err = provider.addDomainRecord(ctx, "AAAA", provider.IPAddrs.IPv6Addr)
				}

				if ipv6Err != nil {
					log.Printf("NEZHA>> Failed to update IPv6 record of domain %s: %v", domain, ipv6Err)
				} else {
					log.Printf("NEZHA>> Update IPv6 record of domain %s succeeded", domain)
					break
				}
			}
		}
	}
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

// deleteDomainRecord 用于安全删除指定类型的解析记录
func (provider *Provider) deleteDomainRecord(ctx context.Context, recType string) error {
	// 同时断言 RecordGetter 与 RecordDeleter，防止提供商因缺少接口导致断言错误
	getter, okGetter := provider.Setter.(libdns.RecordGetter)
	deleter, okDeleter := provider.Setter.(libdns.RecordDeleter)

	if !okGetter || !okDeleter {
		log.Printf("NEZHA>> DNS provider does not support record getting or deletion, safely skipping deletion for %s", recType)
		return nil
	}

	cleanName := func(name string) string {
		return strings.ToLower(strings.TrimSuffix(name, "."))
	}
	cleanPrefix := cleanName(provider.prefix)
	targetRecType := strings.ToUpper(recType)

	// 通过特征匹配识别 Hurricane Electric (HE) 等特殊供应商
	providerType := strings.ToLower(fmt.Sprintf("%T", provider.Setter))
	isHeProvider := strings.Contains(providerType, "he") ||
		strings.Contains(providerType, "hurricane") ||
		strings.Contains(providerType, "dns.he")

	// 针对 HE 供应商：仅返回根域（@），无法枚举子域名
	if isHeProvider {
		log.Printf("NEZHA>> Provider identified as Hurricane Electric (HE) variant, using direct set deletion path for %s.%s", provider.prefix, provider.zone)
		var dummyIP netip.Addr
		if targetRecType == "A" {
			dummyIP = netip.MustParseAddr("0.0.0.0")
		} else {
			dummyIP = netip.MustParseAddr("::")
		}
		_, err := provider.Setter.SetRecords(ctx, provider.zone,
			[]libdns.Record{
				libdns.Address{
					Name: provider.prefix,
					IP:   dummyIP,
					TTL:  time.Minute,
				},
			})
		return err
	}

	// 获取 DNS 记录（针对标准提供商，如 Cloudflare）
	allRecords, err := getter.GetRecords(ctx, provider.zone)
	if err != nil {
		return fmt.Errorf("failed to get DNS records: %w", err)
	}

	cleanZone := cleanName(provider.zone)

	// 筛选和匹配目标记录
	var targetRecords []libdns.Record
	for _, rec := range allRecords {
		rr := rec.RR()
		if strings.ToUpper(rr.Type) != targetRecType {
			continue
		}

		relName := libdns.RelativeName(rr.Name, provider.zone)
		cleanRel := cleanName(relName)
		cleanRRName := cleanName(rr.Name)

		isApex := cleanRel == "" || cleanRel == "@" || cleanRRName == cleanZone

		var matchedName bool
		if cleanPrefix == "" {
			matchedName = isApex
		} else {
			matchedName = cleanRel == cleanPrefix || cleanRRName == cleanPrefix
		}

		if matchedName {
			targetRecords = append(targetRecords, rec)
		}
	}

	// 若未找到对应记录（例如已经成功删除），则直接返回成功
	if len(targetRecords) == 0 {
		log.Printf("NEZHA>> No matching %s record found for deletion under zone %s, already clean", recType, provider.zone)
		return nil
	}

	// 执行实际删除操作
	_, err = deleter.DeleteRecords(ctx, provider.zone, targetRecords)
	if err != nil {
		return fmt.Errorf("deleter.DeleteRecords failed: %w", err)
	}

	log.Printf("NEZHA>> Successfully deleted %d matching %s record(s) for %s.%s", len(targetRecords), recType, provider.prefix, provider.zone)
	return nil
}

func (provider *Provider) splitDomainSOA(ctx context.Context, domain string, overrideServers ...string) (prefix string, zone string, err error) {
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
