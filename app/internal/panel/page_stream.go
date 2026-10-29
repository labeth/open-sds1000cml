package panel

import (
	"time"

	"open-sds/app/internal/panel/wsview"
)

// pageStreamAbandon is how long the web page that asked for the scope's
// stream may go without reading it before the scope takes it as gone: a
// closed tab, a sleeping phone or a lost network sends no word. A hidden
// tab's timers may run once a minute, so it is longer than that.
const pageStreamAbandon = 2 * time.Minute

// pageClaim is a stream the web page asked for and reads, and the BUS view
// (or workspace) the scope showed before it.
type pageClaim struct {
	on        bool
	read      time.Time
	ws        wsview.WS
	busVisual bool
}

// PageStream starts the stream for the web page, which then reads it, or
// ends it: the BUS sheet's Stream row, held by the page. A stream the scope
// ran already stays the scope's; a key on the scope takes the page's.
// TRLC-LINKS: REQ-SDS-228, REQ-SDS-278
func (c *Controller) PageStream(on bool) SetResult {
	return c.remoteCall("bus.stream", func() SetResult {
		c.mu.Lock()
		had := c.ui.ws == wsview.Bus && c.ui.busStream
		claim := pageClaim{on: true, read: c.now(), ws: c.ui.ws, busVisual: c.ui.busVisual}
		held := c.page
		c.mu.Unlock()
		v := 0
		if on {
			v = 1
		}
		res := c.setRow("bus.stream", v, "", true)
		c.mu.Lock()
		switch {
		case !res.OK || !on:
			c.page = pageClaim{}
		case !had:
			c.page = claim
		case c.page.on:
			c.page.read = c.now()
		}
		c.mu.Unlock()
		if res.OK && !on && held.on {
			c.pageViewBack(held)
		}
		return res
	})
}

// PageLeave is the web page going (its leave beacon): a stream it still
// holds ends and the scope goes back to the view it showed; one the scope
// took over (a key pressed on it) stays.
// TRLC-LINKS: REQ-SDS-228, REQ-SDS-278
func (c *Controller) PageLeave() SetResult {
	c.mu.Lock()
	held := c.page.on
	c.mu.Unlock()
	if !held {
		return SetResult{OK: true, Row: "bus.stream"}
	}
	return c.PageStream(false)
}

// noteStreamRead is the web page reading the stream: it is still there.
// TRLC-LINKS: REQ-SDS-228
func (c *Controller) noteStreamRead() {
	c.mu.Lock()
	if c.page.on {
		c.page.read = c.now()
	}
	c.mu.Unlock()
}

// checkPageStream ends a stream the web page asked for and has not read for
// pageStreamAbandon, and puts the scope back on the view it showed before;
// a stream ended otherwise drops the claim.
// TRLC-LINKS: REQ-SDS-228
func (c *Controller) checkPageStream() {
	c.mu.Lock()
	p := c.page
	streaming := c.ui.ws == wsview.Bus && c.ui.busStream
	gone := p.on && streaming && c.now().Sub(p.read) >= pageStreamAbandon
	if p.on && (!streaming || gone) {
		c.page = pageClaim{}
	}
	c.mu.Unlock()
	if !gone {
		return
	}
	c.logf("panel: the web page's stream unread for %s - ended", pageStreamAbandon)
	c.showBusView(false)
	c.pageViewBack(p)
	c.toast("The web page that asked for the stream is gone: stream ended", 2500*time.Millisecond)
	c.afterInput()
}

// pageViewBack puts the scope back on the view it showed before the page's
// stream (the stream ended).
// TRLC-LINKS: REQ-SDS-228
func (c *Controller) pageViewBack(p pageClaim) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ui.ws != wsview.Bus || c.ui.busStream || c.ui.busEye {
		return // the scope moved on meanwhile
	}
	c.ui.busVisual = p.busVisual
	if p.ws != wsview.Bus {
		c.ui.views[c.ui.ws] = c.viewMode
		c.ui.ws, c.ui.posJob = p.ws, false
		c.viewMode = c.ui.views[p.ws]
	}
}
