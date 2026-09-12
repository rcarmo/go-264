// Command decodeaudio streams PCM WAV to raw little-endian signed 16-bit PCM.
// Input file ownership and output storage policy belong to this application.
package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/rcarmo/go-264/audio"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	rate := flag.Int("rate", 16000, "output sample rate (Hz)")
	channels := flag.Int("channels", 1, "output channels (1 or 2)")
	flag.Parse()
	if flag.NArg() != 1 {
		return fmt.Errorf("usage: decodeaudio [-rate 16000] [-channels 1] input.wav > output.s16le")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	f, err := os.Open(flag.Arg(0))
	if err != nil {
		return err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	dec, err := audio.Open(ctx, f, stat.Size(), audio.Options{TargetRate: *rate, TargetChannels: *channels})
	if err != nil {
		return err
	}
	defer dec.Close()
	b := make([]int16, 4096)
	raw := make([]byte, 8192)
	for {
		n, _, err := dec.ReadPCM(ctx, b)
		for i, v := range b[:n] {
			binary.LittleEndian.PutUint16(raw[2*i:], uint16(v))
		}
		if n > 0 {
			if _, e := os.Stdout.Write(raw[:2*n]); e != nil {
				return e
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
