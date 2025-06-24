//go:build linux
// +build linux

package output

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/ecobee/goalsa"
)

type output struct {
	dev         *alsa.PlaybackDevice
	samplesChan chan []float32
	samplesBuf  []float32
	sampleRate  int
	channels    int
	mu          sync.Mutex
	closed      bool
}

func (o *output) init() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.dev != nil {
		o.dev.Close()
		o.dev = nil
	}
	params := alsa.BufferParams{
		BufferFrames: 4096,
		PeriodFrames: 1024,
		Periods:      4,
	}
	dev, err := alsa.NewPlaybackDevice("default", o.channels, alsa.FormatFloat32LE, o.sampleRate, params)
	if err != nil {
		return fmt.Errorf("alsa: %w", err)
	}
	o.dev = dev
	o.samplesChan = make(chan []float32, 8)
	o.closed = false
	go o.writer()
	return nil
}

func (o *output) writer() {
	buf := make([]float32, 1024*o.channels)
	for {
		if o.closed {
			return
		}
		// 优先输出缓存
		i := copy(buf, o.samplesBuf)
		o.samplesBuf = o.samplesBuf[i:]

		for i < len(buf) {
			select {
			case samples := <-o.samplesChan:
				n := copy(buf[i:], samples)
				o.samplesBuf = samples[n:]
				i += n
			case <-time.After(100 * time.Millisecond):
				// 填充静音
				for j := i; j < len(buf); j++ {
					buf[j] = 0
				}
				break
			}
		}
		if o.dev != nil {
			_, err := o.dev.Write(buf)
			if err != nil {
				log.Println("alsa write error:", err)
				o.restart()
				time.Sleep(100 * time.Millisecond)
			}
		}
	}
}

func (o *output) restart() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.dev != nil {
		o.dev.Close()
	}
	params := alsa.BufferParams{
		BufferFrames: 4096,
		PeriodFrames: 1024,
		Periods:      4,
	}
	dev, err := alsa.NewPlaybackDevice("default", o.channels, alsa.FormatFloat32LE, o.sampleRate, params)
	if err != nil {
		log.Println("alsa restart error:", err)
		return
	}
	o.dev = dev
	log.Println("alsa device restarted")
}

func get(sampleRate, channels int) (Output, error) {
	o := &output{
		sampleRate: sampleRate,
		channels:   channels,
	}
	err := o.init()
	return o, err
}

func (o *output) Push(samples []float32) {
	select {
	case o.samplesChan <- samples:
	case <-time.After(100 * time.Millisecond):
		log.Println("alsa output timeout, restarting alsa device")
		o.restart()
		go func() { o.samplesChan <- samples }()
	}
}

func (o *output) Start() {
	// ALSA不需要显式Start，但可预热缓冲
	s := make([]float32, 1024*o.channels)
	go o.Push(s)
}

func (o *output) Stop() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.closed = true
	if o.dev != nil {
		o.dev.Close()
	}
}
