package input

import (
	"bufio"
	"os"
	"sync"
	"time"

	"github.com/childe/gohangout/codec"
	"github.com/childe/gohangout/topology"
	"k8s.io/klog/v2"
)

// StdinConfig defines the configuration structure for Stdin input
type StdinConfig struct {
	Codec string `json:"codec"`
}

type StdinInput struct {
	config  map[any]any
	decoder codec.Decoder

	scanner  *bufio.Scanner
	scanLock sync.Mutex

	stop bool
}

// 1.每个 Input实现 都会初始化注册到map里
func init() {
	Register("Stdin", newStdinInput)
}

func newStdinInput(config map[any]any) topology.Input {
	// Parse configuration using SafeDecodeConfig helper
	var stdinConfig StdinConfig
	stdinConfig.Codec = "plain" // set default value

	SafeDecodeConfig("Stdin", config, &stdinConfig)

	p := &StdinInput{
		config:  config,
		decoder: codec.NewDecoder(stdinConfig.Codec),
		scanner: bufio.NewScanner(os.Stdin),
	}

	return p // 类型实现了接口，所以可以返回这个类型
}

func (p *StdinInput) ReadOneEvent() map[string]any {
	p.scanLock.Lock()
	defer p.scanLock.Unlock()

	if p.scanner.Scan() { // 标准输入是读取控制台的数据
		t := p.scanner.Bytes()
		msg := make([]byte, len(t))
		copy(msg, t)
		return p.decoder.Decode(msg)
	}
	if err := p.scanner.Err(); err != nil {
		klog.Errorf("stdin scan error: %v", err)
	} else {
		// EOF here. when stdin is closed by C-D, cpu will raise up to 100% if not sleep
		time.Sleep(time.Millisecond * 1000)
	}
	return nil
}

func (p *StdinInput) Shutdown() {
	// what we need is to stop emit new event; close messages or not is not important
	p.stop = true
}
