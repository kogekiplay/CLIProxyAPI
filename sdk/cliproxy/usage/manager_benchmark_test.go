package usage

import (
	"context"
	"testing"
)

type acknowledgedUsagePlugin struct {
	done chan struct{}
}

func (p *acknowledgedUsagePlugin) HandleUsage(context.Context, Record) {
	p.done <- struct{}{}
}

func BenchmarkManagerPublishSerial(b *testing.B) {
	manager := NewManager(512)
	plugin := &acknowledgedUsagePlugin{done: make(chan struct{}, 1)}
	manager.Register(plugin)
	manager.Start(context.Background())
	defer manager.Stop()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		manager.Publish(context.Background(), Record{})
		<-plugin.done
	}
}
