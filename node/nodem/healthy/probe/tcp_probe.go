package probe

import (
	"context"
	"github.com/goodrain/rainbond/node/nodem/service"
	"github.com/goodrain/rainbond/util"
	"time"
	"net"
	"github.com/goodrain/rainbond/node/nodem/client"
)


type TcpProbe struct {
	Name         string
	Address      string
	ResultsChan  chan *service.HealthStatus
	Ctx          context.Context
	Cancel       context.CancelFunc
	TimeInterval int
	HostNode     *client.HostNode
	MaxErrorsTime int
}

func (h *TcpProbe) TcpCheck() {

	util.Exec(h.Ctx, func() error {
		HealthMap := GetTcpHealth(h.Address,h.MaxErrorsTime)
		result := &service.HealthStatus{
			Name:   h.Name,
			Status: HealthMap["status"],
			Info:   HealthMap["info"],
		}
		if HealthMap["status"] != service.Stat_healthy {
			v := client.NodeCondition{
				Type:    client.NodeConditionType(h.Name),
				Status:  client.ConditionFalse,
				LastHeartbeatTime:time.Now(),
				LastTransitionTime:time.Now(),
				Message: result.Info,
			}
			h.HostNode.UpdataCondition(v)
		}
		if HealthMap["status"] == service.Stat_healthy{
			v := client.NodeCondition{
				Type:client.NodeConditionType(h.Name),
				Status:client.ConditionTrue,
				LastHeartbeatTime:time.Now(),
				LastTransitionTime:time.Now(),
			}
			h.HostNode.UpdataCondition(v)
		}
		h.ResultsChan <- result
		return nil
	}, time.Second*time.Duration(h.TimeInterval))
}

func GetTcpHealth(address string, maxErrorsTime int) map[string]string {
	var result map[string]string
	for num:=0; num <= maxErrorsTime;num++{
		conn, err := net.Dial("tcp", address)
		if err != nil {
			result = map[string]string{"status": service.Stat_death, "info": "Tcp connection error"}
			time.Sleep(1*time.Second)
			continue
		}
		defer conn.Close()
		return map[string]string{"status": service.Stat_healthy, "info": "service health"}
	}
	return result
}
