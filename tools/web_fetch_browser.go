package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

func fetchWebPageWithBrowser(ctx context.Context, rawURL string) (webFetchPage, error) {
	allocatorOptions := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	allocatorOptions = append(allocatorOptions,
		chromedp.Flag("headless", "new"),
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
	)
	allocatorCtx, cancelAllocator := chromedp.NewExecAllocator(ctx, allocatorOptions...)
	defer cancelAllocator()
	browserCtx, cancelBrowser := chromedp.NewContext(allocatorCtx)
	defer cancelBrowser()

	var renderedHTML string
	response, err := chromedp.RunResponse(browserCtx,
		chromedp.ActionFunc(setBrowserIdentity),
		emulation.SetAutomationOverride(false),
		chromedp.Navigate(rawURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.OuterHTML("html", &renderedHTML, chromedp.ByQuery),
	)
	if err != nil {
		return webFetchPage{}, fmt.Errorf("render page: %w", err)
	}
	if response == nil {
		return webFetchPage{}, fmt.Errorf("render page: main document response is unavailable")
	}

	contentType := strings.TrimSpace(response.MimeType)
	if response.Charset != "" {
		contentType += "; charset=" + response.Charset
	}
	return newWebFetchPage(int(response.Status), contentType, []byte(renderedHTML)), nil
}

func setBrowserIdentity(ctx context.Context) error {
	_, _, _, userAgent, _, err := browser.GetVersion().Do(ctx)
	if err != nil {
		return fmt.Errorf("read browser version: %w", err)
	}
	userAgent = strings.ReplaceAll(userAgent, "HeadlessChrome/", "Chrome/")
	if err := emulation.SetUserAgentOverride(userAgent).Do(ctx); err != nil {
		return fmt.Errorf("set browser user agent: %w", err)
	}
	return nil
}
