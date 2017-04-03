package exchange

import (
	"sync"

	"golang.org/x/net/context"
	"google.golang.org/grpc"

	dh "deephealth"
	pb "deephealth/build/gen"
	dt "deephealth/types"
)

const (
	etag = "exchange"
)

type IgnoreSet map[dt.EntityId]bool

type ExchangeProtocol struct {
	Id   dt.EntityId // my id
	Addr string      // my addr

	Peers            map[dt.EntityId]string    // all peers' id and address
	SkipSubjectPeers map[dt.EntityId]IgnoreSet // skip sending reports about a subject to certain peers

	Clients map[dt.EntityId]pb.HealthServiceClient // clients to all peers

	me *pb.Peer
	mu *sync.Mutex
}

var _ dt.HealthExchange = new(ExchangeProtocol)

func NewExchangeProtocol(config *dt.HealthServerConfig) *ExchangeProtocol {
	return &ExchangeProtocol{
		Id:               config.Owner,
		Addr:             config.Addr,
		Peers:            config.Peers,
		SkipSubjectPeers: make(map[dt.EntityId]IgnoreSet),
		Clients:          make(map[dt.EntityId]pb.HealthServiceClient),
		me:               &pb.Peer{string(config.Owner), config.Addr},
		mu:               &sync.Mutex{},
	}
}

func (self *ExchangeProtocol) Propagate(report *dt.Report) error {
	var ferr error
	pbr := dt.ReportToPb(report)
	request := &pb.LearnReportRequest{Source: self.me, Report: pbr}
	self.mu.Lock()
	ignoreset, ok := self.SkipSubjectPeers[report.Subject]
	if !ok {
		ignoreset = nil
	}
	self.mu.Unlock()
	for peer, addr := range self.Peers {
		if peer == self.Id {
			continue // skip send to self
		}
		if ignoreset != nil {
			_, ok := ignoreset[peer]
			if ok {
				continue
			}
		}
		client, err := self.getOrMakeClient(peer)
		if err != nil {
			ferr = err
			continue
		}
		reply, err := client.LearnReport(context.Background(), request)
		if err != nil {
			ferr = err
			continue
		}
		dh.LogD(etag, "propagated report to peer %s at %s", peer, addr)
		if reply.Result == pb.LearnReportReply_IGNORED {
			self.mu.Lock()
			if ignoreset == nil {
				ignoreset = make(IgnoreSet)
				self.SkipSubjectPeers[report.Subject] = ignoreset
			}
			ignoreset[peer] = true
			self.mu.Unlock()
			dh.LogD(etag, "ignore peer %s about subject %s in the future", peer, report.Subject)
		}
	}
	return ferr
}

func (self *ExchangeProtocol) getOrMakeClient(peer dt.EntityId) (pb.HealthServiceClient, error) {
	client, ok := self.Clients[peer]
	if ok {
		return client, nil
	}
	addr := self.Peers[peer]
	conn, err := grpc.Dial(addr, grpc.WithInsecure())
	if err != nil {
		return nil, err
	}
	client = pb.NewHealthServiceClient(conn)
	self.Clients[peer] = client
	return client, nil
}
