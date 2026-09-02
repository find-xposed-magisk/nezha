package model

import (
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/nezhahq/nezha/pkg/utils"
)

const (
	RuleCoverAll = iota
	RuleCoverIgnoreAll
)

// MaxAlertRuleDuration is the largest duration that can be converted to int
// on every architecture supported by Go. Alert rules are persisted as uint64,
// but their sampling windows use int indexes; keeping the public bound at
// MaxInt32 prevents a crafted value from wrapping during that conversion.
const MaxAlertRuleDuration uint64 = 1<<31 - 1

// MaxAlertRuleCycleInterval also leaves room for the largest integer
// multiplication performed by the calendar helpers (weeks become 7*interval)
// on 32-bit targets.
const MaxAlertRuleCycleInterval uint64 = (1<<31 - 1) / 7

type NResult struct {
	N uint64
}

type Rule struct {
	// 指标类型，cpu、gpu/gpu_max、memory、swap、disk、net_in_speed、net_out_speed
	// net_all_speed、transfer_in、transfer_out、transfer_all、offline
	// transfer_in_cycle、transfer_out_cycle、transfer_all_cycle
	Type          string          `json:"type"`
	Min           float64         `json:"min,omitempty" validate:"optional"`                                                        // 最小阈值 (百分比、字节 kb ÷ 1024)
	Max           float64         `json:"max,omitempty" validate:"optional"`                                                        // 最大阈值 (百分比、字节 kb ÷ 1024)
	CycleStart    *time.Time      `json:"cycle_start,omitempty" validate:"optional"`                                                // 流量统计的开始时间
	CycleInterval uint64          `json:"cycle_interval,omitempty" validate:"optional"`                                             // 流量统计周期
	CycleUnit     string          `json:"cycle_unit,omitempty" enums:"hour,day,week,month,year" validate:"optional" default:"hour"` // 流量统计周期单位，默认hour,可选(hour, day, week, month, year)
	Duration      uint64          `json:"duration,omitempty" validate:"optional"`                                                   // 持续时间 (秒)
	Cover         uint64          `json:"cover"`                                                                                    // 覆盖范围 RuleCoverAll/IgnoreAll
	Ignore        map[uint64]bool `json:"ignore,omitempty" validate:"optional"`                                                     // 覆盖范围的排除

	// 只作为缓存使用，记录下次该检测的时间
	NextTransferAt  map[uint64]time.Time `json:"-"`
	LastCycleStatus map[uint64]bool      `json:"-"`
}

func percentage(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(used) * 100 / float64(total)
}

// IsSupportedType reports whether the rule type has an evaluator. Keep this
// list in lockstep with Snapshot's switch so untrusted strings cannot enter the
// persisted alert pipeline and silently acquire fallback semantics.
func (u *Rule) IsSupportedType() bool {
	if u == nil {
		return false
	}
	switch u.Type {
	case "cpu", "gpu", "gpu_max", "memory", "swap", "disk",
		"net_in_speed", "net_out_speed", "net_all_speed",
		"transfer_in", "transfer_out", "transfer_all", "offline",
		"transfer_in_cycle", "transfer_out_cycle", "transfer_all_cycle",
		"load1", "load5", "load15", "tcp_conn_count", "udp_conn_count",
		"process_count", "temperature_max":
		return true
	default:
		return false
	}
}

// DurationInt converts the persisted duration without allowing uint64-to-int
// truncation. Callers must handle ok=false as an invalid rule.
func (u *Rule) DurationInt() (duration int, ok bool) {
	if u == nil || u.Duration > MaxAlertRuleDuration {
		return 0, false
	}
	return int(u.Duration), true
}

// HasSafeCycleConfiguration guards every value that the cycle evaluator later
// converts or dereferences. It intentionally does not reject an empty unit,
// which is the documented legacy spelling for hours.
func (u *Rule) HasSafeCycleConfiguration() bool {
	return u != nil && u.IsTransferDurationRule() && u.CycleStart != nil &&
		u.CycleInterval > 0 && u.CycleInterval <= MaxAlertRuleCycleInterval
}

// Snapshot 未通过规则返回 false, 通过返回 true
func (u *Rule) Snapshot(cycleTransferStats *CycleTransferStats, server *Server, db *gorm.DB) bool {
	if u == nil || server == nil || !u.IsSupportedType() {
		return true
	}
	if u.IsTransferDurationRule() && (!u.HasSafeCycleConfiguration() || cycleTransferStats == nil) {
		return true
	}

	// 监控全部但是排除了此服务器
	if u.Cover == RuleCoverAll && u.Ignore[server.ID] {
		return true
	}
	// 忽略全部但是指定监控了此服务器
	if u.Cover == RuleCoverIgnoreAll && !u.Ignore[server.ID] {
		return true
	}

	// 循环区间流量检测 · 短期无需重复检测
	if u.IsTransferDurationRule() && u.NextTransferAt[server.ID].After(time.Now()) {
		return u.LastCycleStatus[server.ID]
	}

	var src float64
	runtime := server.RuntimeSnapshot()
	if runtime.State == nil {
		return false
	}
	state := runtime.State

	switch u.Type {
	case "cpu":
		src = float64(state.CPU)
	case "gpu", "gpu_max":
		if len(state.GPU) == 0 {
			return true
		}
		src = slices.Max(state.GPU)
	case "memory":
		if runtime.Host == nil {
			return false
		}
		src = percentage(state.MemUsed, runtime.Host.MemTotal)
	case "swap":
		if runtime.Host == nil {
			return false
		}
		src = percentage(state.SwapUsed, runtime.Host.SwapTotal)
	case "disk":
		if runtime.Host == nil {
			return false
		}
		src = percentage(state.DiskUsed, runtime.Host.DiskTotal)
	case "net_in_speed":
		src = float64(state.NetInSpeed)
	case "net_out_speed":
		src = float64(state.NetOutSpeed)
	case "net_all_speed":
		src = float64(state.NetOutSpeed + state.NetOutSpeed)
	case "transfer_in":
		src = float64(state.NetInTransfer)
	case "transfer_out":
		src = float64(state.NetOutTransfer)
	case "transfer_all":
		src = float64(state.NetOutTransfer + state.NetInTransfer)
	case "offline":
		if runtime.LastActive.IsZero() {
			src = 0
		} else {
			src = float64(runtime.LastActive.Unix())
		}
	case "transfer_in_cycle":
		src = float64(utils.SubUintChecked(state.NetInTransfer, runtime.PrevTransferInSnapshot))
		if u.CycleInterval != 0 {
			var res NResult
			db.Model(&Transfer{}).Select("SUM(`in`) AS n").Where("datetime(`created_at`) >= datetime(?) AND server_id = ?", u.GetTransferDurationStart().UTC(), server.ID).Scan(&res)
			src += float64(res.N)
		}
	case "transfer_out_cycle":
		src = float64(utils.SubUintChecked(state.NetOutTransfer, runtime.PrevTransferOutSnapshot))
		if u.CycleInterval != 0 {
			var res NResult
			db.Model(&Transfer{}).Select("SUM(`out`) AS n").Where("datetime(`created_at`) >= datetime(?) AND server_id = ?", u.GetTransferDurationStart().UTC(), server.ID).Scan(&res)
			src += float64(res.N)
		}
	case "transfer_all_cycle":
		src = float64(utils.SubUintChecked(state.NetOutTransfer, runtime.PrevTransferOutSnapshot) + utils.SubUintChecked(state.NetInTransfer, runtime.PrevTransferInSnapshot))
		if u.CycleInterval != 0 {
			var res NResult
			db.Model(&Transfer{}).Select("SUM(`in`+`out`) AS n").Where("datetime(`created_at`) >= datetime(?) AND server_id = ?", u.GetTransferDurationStart().UTC(), server.ID).Scan(&res)
			src += float64(res.N)
		}
	case "load1":
		src = state.Load1
	case "load5":
		src = state.Load5
	case "load15":
		src = state.Load15
	case "tcp_conn_count":
		src = float64(state.TcpConnCount)
	case "udp_conn_count":
		src = float64(state.UdpConnCount)
	case "process_count":
		src = float64(state.ProcessCount)
	case "temperature_max":
		var temp []float64
		for _, tempStat := range state.Temperatures {
			if tempStat.Temperature != 0 {
				temp = append(temp, tempStat.Temperature)
			}
		}
		if len(temp) == 0 {
			return true
		}
		src = slices.Max(temp)
	default:
		return true
	}

	// 循环区间流量检测 · 更新下次需要检测时间
	if u.IsTransferDurationRule() {
		seconds := max(1800*((u.Max-src)/u.Max), 180)
		if u.NextTransferAt == nil {
			u.NextTransferAt = make(map[uint64]time.Time)
		}
		if u.LastCycleStatus == nil {
			u.LastCycleStatus = make(map[uint64]bool)
		}
		u.NextTransferAt[server.ID] = time.Now().Add(time.Second * time.Duration(seconds))
		if (u.Max > 0 && src > u.Max) || (u.Min > 0 && src < u.Min) {
			u.LastCycleStatus[server.ID] = false
		} else {
			u.LastCycleStatus[server.ID] = true
		}
		if cycleTransferStats.ServerName[server.ID] != server.Name {
			cycleTransferStats.ServerName[server.ID] = server.Name
		}
		cycleTransferStats.Transfer[server.ID] = uint64(src)
		cycleTransferStats.NextUpdate[server.ID] = u.NextTransferAt[server.ID]
		// 自动更新周期流量展示起止时间
		cycleTransferStats.From = u.GetTransferDurationStart()
		cycleTransferStats.To = u.GetTransferDurationEnd()
	}

	if u.Type == "offline" && float64(time.Now().Unix())-src > 6 {
		return false
	} else if (u.Max > 0 && src > u.Max) || (u.Min > 0 && src < u.Min) {
		return false
	}

	return true
}

// IsTransferDurationRule 判断该规则是否属于周期流量规则 属于则返回true
func (u *Rule) IsTransferDurationRule() bool {
	if u == nil {
		return false
	}
	switch u.Type {
	case "transfer_in_cycle", "transfer_out_cycle", "transfer_all_cycle":
		return true
	default:
		return false
	}
}

func (u *Rule) IsOfflineRule() bool {
	return u != nil && u.Type == "offline"
}

// GetTransferDurationStart 获取周期流量的起始时间
func (u *Rule) GetTransferDurationStart() time.Time {
	// Accept uppercase and lowercase
	unit := strings.ToLower(u.CycleUnit)
	startTime := *u.CycleStart
	var nextTime time.Time
	switch unit {
	case "year":
		nextTime = startTime.AddDate(int(u.CycleInterval), 0, 0)
		for time.Now().After(nextTime) {
			startTime = nextTime
			nextTime = nextTime.AddDate(int(u.CycleInterval), 0, 0)
		}
	case "month":
		nextTime = startTime.AddDate(0, int(u.CycleInterval), 0)
		for time.Now().After(nextTime) {
			startTime = nextTime
			nextTime = nextTime.AddDate(0, int(u.CycleInterval), 0)
		}
	case "week":
		nextTime = startTime.AddDate(0, 0, 7*int(u.CycleInterval))
		for time.Now().After(nextTime) {
			startTime = nextTime
			nextTime = nextTime.AddDate(0, 0, 7*int(u.CycleInterval))
		}
	case "day":
		nextTime = startTime.AddDate(0, 0, int(u.CycleInterval))
		for time.Now().After(nextTime) {
			startTime = nextTime
			nextTime = nextTime.AddDate(0, 0, int(u.CycleInterval))
		}
	default:
		// For hour unit or not set.
		interval := 3600 * int64(u.CycleInterval)
		startTime = time.Unix(u.CycleStart.Unix()+(time.Now().Unix()-u.CycleStart.Unix())/interval*interval, 0)
	}

	return startTime
}

// GetTransferDurationEnd 获取周期流量结束时间
func (u *Rule) GetTransferDurationEnd() time.Time {
	// Accept uppercase and lowercase
	unit := strings.ToLower(u.CycleUnit)
	startTime := *u.CycleStart
	var nextTime time.Time
	switch unit {
	case "year":
		nextTime = startTime.AddDate(int(u.CycleInterval), 0, 0)
		for time.Now().After(nextTime) {
			startTime = nextTime
			nextTime = nextTime.AddDate(int(u.CycleInterval), 0, 0)
		}
	case "month":
		nextTime = startTime.AddDate(0, int(u.CycleInterval), 0)
		for time.Now().After(nextTime) {
			startTime = nextTime
			nextTime = nextTime.AddDate(0, int(u.CycleInterval), 0)
		}
	case "week":
		nextTime = startTime.AddDate(0, 0, 7*int(u.CycleInterval))
		for time.Now().After(nextTime) {
			startTime = nextTime
			nextTime = nextTime.AddDate(0, 0, 7*int(u.CycleInterval))
		}
	case "day":
		nextTime = startTime.AddDate(0, 0, int(u.CycleInterval))
		for time.Now().After(nextTime) {
			startTime = nextTime
			nextTime = nextTime.AddDate(0, 0, int(u.CycleInterval))
		}
	default:
		// For hour unit or not set.
		interval := 3600 * int64(u.CycleInterval)
		startTime = time.Unix(u.CycleStart.Unix()+(time.Now().Unix()-u.CycleStart.Unix())/interval*interval, 0)
		nextTime = time.Unix(startTime.Unix()+interval, 0)
	}

	return nextTime
}
