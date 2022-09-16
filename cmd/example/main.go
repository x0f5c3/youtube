package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lithdew/nicehttp"
	"github.com/valyala/fasthttp"
	"github.com/x0f5c3/pterm"

	"github.com/lithdew/youtube"
)

func fastHandleTunnel(ctx *fasthttp.RequestCtx) {
	pterm.Debug.Printfln("Dialing dest %s", ctx.Request.Host())
	destConn, err := fasthttp.DialTimeout(string(ctx.Request.Host()), 10*time.Second)
	if err != nil {
		ctx.Error(err.Error(), fasthttp.StatusServiceUnavailable)
		return
	}
	ctx.Response.SetStatusCode(200)
	ctx.Hijack(func(clientConn net.Conn) {
		go transfer(destConn, clientConn)
		go transfer(clientConn, destConn)
	})
}

func handleTunnel(w http.ResponseWriter, r *http.Request) {
	destConn, err := net.DialTimeout("tcp", r.Host, 10*time.Second)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	}
	go transfer(destConn, clientConn)
	go transfer(clientConn, destConn)
}

func transfer(destination io.WriteCloser, source io.ReadCloser) {
	defer destination.Close()
	defer source.Close()
	io.Copy(destination, source)
}

func handleHTTP(w http.ResponseWriter, req *http.Request) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()
	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}
func fastHandleHTTP(ctx *fasthttp.RequestCtx) {
	err := fasthttp.DoRedirects(&ctx.Request, &ctx.Response, 30)
	if err != nil {
		ctx.Error(err.Error(), fasthttp.StatusServiceUnavailable)
		return
	}
	ctx.SetStatusCode(200)

}

func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

func fastCopyHeader(dst, src *fasthttp.RequestHeader) {
	src.CopyTo(dst)
}

func check(err error) {
	if err != nil {
		pterm.Fatal.Println(err)
	}
}

func printMsg(format string, a ...interface{}) {
	pterm.Info.Printfln(format, a...)
}

func printOnErr(err error) {
	pterm.Error.PrintOnError(err)
}

func fatalOnErr(err ...interface{}) {
	pterm.Fatal.PrintOnError(err...)
}

func handleRequest(w http.ResponseWriter, req *http.Request) {
	userAgent := req.UserAgent()
	printMsg("User-Agent: %s", userAgent)
	headers := req.Header
	printMsg("Headers: %#v", headers)
	cookies := req.Cookies()
	printMsg("Cookies: %#v", cookies)
	query := req.URL.Query().Encode()
	printMsg("Query: %s", query)
	req.URL.Host = "www.youtube.com"
	req.URL.Scheme = "https"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		w.WriteHeader(500)
		_, err = w.Write([]byte(err.Error()))
		if err != nil {
			return
		}
	}
	err = resp.Write(w)
	if err != nil {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(err.Error()))
		return
	}
	w.WriteHeader(resp.StatusCode)
}

func main() {
	// Search for the song Animus Vox by The Glitch Mob.
	s := fasthttp.RequestHandler(func(ctx *fasthttp.RequestCtx) {
		if bytes.Equal(ctx.Request.Header.Method(), []byte(fasthttp.MethodConnect)) {
			fastHandleTunnel(ctx)
		} else {
			fastHandleHTTP(ctx)
		}
	})
	srv := &fasthttp.Server{Handler: s}
	closeChan := make(chan os.Signal, 1)
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT)
		c := <-sigChan
		pterm.Warning.Printfln("Received signal %d - shutting down...", c)
		err := srv.Shutdown()
		closeChan <- c
		if err != nil {
			pterm.Error.Printfln("Shutdown error: %s", err)
			return
		}
		pterm.Success.Println("Shutdown successful")
	}()
	go func() {
		err := srv.ListenAndServe()
		if !errors.Is(err, http.ErrServerClosed) {
			pterm.Error.Printfln("Server error: %s", err)
		}
		<-closeChan
		return
	}()
	results, err := youtube.Search("$uicideboy$", 0)
	check(err)

	fmt.Printf("Got %d search result(s).\n\n", results.Hits)

	if len(results.Items) == 0 {
		check(fmt.Errorf("got zero search results"))
	}

	b, err := json.Marshal(&results)
	pterm.Fatal.PrintOnError(err)
	pterm.Fatal.PrintOnError(os.WriteFile("SearchResultAnimuxVox.json", b, 0666))
	// Get the first search result and print out its details.

	details := results.Items[0]

	fmt.Printf(
		"ID: %q\n\nTitle: %q\nAuthor: %q\nDuration: %q\n\nView Count: %q\nLikes: %d\nDislikes: %d\n\n",
		details.ID,
		details.Title,
		details.Author,
		details.Duration,
		details.Views,
		details.Likes,
		details.Dislikes,
	)

	// Instantiate a player for the first search result.

	player, err := youtube.Load(details.ID)
	check(err)
	st := player.Streams.V().MarshalTo(nil)
	pterm.Fatal.PrintOnErrorf("Failed to write Streams.v value to JSON %s", os.WriteFile("Streams_v.json", st, 0666))
	// Fetch audio-only direct link.

	stream, ok := player.SourceFormats().AudioOnly().BestAudio()
	if !ok {
		check(fmt.Errorf("no audio-only stream available"))
	}
	st, err = json.Marshal(&stream)
	pterm.Fatal.PrintOnErrorf("Failed to marshal JSON %s", err)
	pterm.Fatal.PrintOnErrorf("Failed to write Format value to JSON %s", os.WriteFile("Format.json", st, 0666))

	audioOnlyFilename := "audio." + stream.FileExtension()

	audioOnlyURL, err := player.ResolveURL(stream)
	check(err)

	fmt.Printf("Audio-only direct link: %q\n", audioOnlyURL)

	// Fetch video-only direct link.

	stream, ok = player.SourceFormats().VideoOnly().BestVideo()
	if !ok {
		check(fmt.Errorf("no video-only stream available"))
	}

	videoOnlyFilename := "video." + stream.FileExtension()

	videoOnlyURL, err := player.ResolveURL(stream)
	check(err)

	fmt.Printf("Video-only direct link: %q\n", videoOnlyURL)

	// Fetch muxed video/audio direct link.

	stream, ok = player.MuxedFormats().BestVideo()
	if !ok {
		check(fmt.Errorf("no muxed stream available"))
	}

	muxedFilename := "muxed." + stream.FileExtension()

	muxedURL, err := player.ResolveURL(stream)
	check(err)

	fmt.Printf("Muxed (video/audio) direct link: %q\n", muxedURL)

	// Download all the links.

	check(nicehttp.DownloadFile(audioOnlyFilename, audioOnlyURL))
	check(nicehttp.DownloadFile(videoOnlyFilename, videoOnlyURL))
	check(nicehttp.DownloadFile(muxedFilename, muxedURL))
}
