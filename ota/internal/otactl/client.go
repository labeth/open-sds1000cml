package otactl

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// Client wraps a Transport with the high-level device operations (file
// transfer, app/agent OTA) that are multi-step over the raw RPC.
type Client struct {
	T Transport
}

func (c *Client) Call(cmd string, args any, timeout time.Duration) (json.RawMessage, error) {
	resp, err := c.T.Call(cmd, args, timeout)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return resp.Data, fmt.Errorf("device error: %s", resp.Err)
	}
	return resp.Data, nil
}

// PutFile streams a local file to an absolute device path via put.begin/
// put.chunk/put.commit and returns the device-side sha256.
func (c *Client) PutFile(localPath, devicePath string, mode uint32, chunk int, progress func(done, total int64)) (string, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := fi.Size()

	// Local sha256 for end-to-end verification.
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	if mode == 0 {
		mode = uint32(fi.Mode().Perm())
	}

	beginRaw, err := c.Call("put.begin", map[string]any{
		"dest": devicePath, "size": size, "sha256": sum, "mode": mode,
	}, 30*time.Second)
	if err != nil {
		return "", err
	}
	var begin struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(beginRaw, &begin)
	if begin.ID == "" {
		return "", fmt.Errorf("put.begin returned no id")
	}

	if chunk <= 0 {
		chunk = 128 * 1024
	}
	buf := make([]byte, chunk)
	var off int64
	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			_, err := c.Call("put.chunk", map[string]any{
				"id":     begin.ID,
				"offset": off,
				"data":   base64.StdEncoding.EncodeToString(buf[:n]),
			}, 60*time.Second)
			if err != nil {
				return "", err
			}
			off += int64(n)
			if progress != nil {
				progress(off, size)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", rerr
		}
	}

	commitRaw, err := c.Call("put.commit", map[string]any{"id": begin.ID}, 60*time.Second)
	if err != nil {
		return "", err
	}
	var commit struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	}
	_ = json.Unmarshal(commitRaw, &commit)
	if commit.SHA256 != sum {
		return commit.SHA256, fmt.Errorf("sha mismatch: local %s, device %s", sum, commit.SHA256)
	}
	return commit.SHA256, nil
}

// GetFile pulls a device file to a local path via the get RPC.
func (c *Client) GetFile(devicePath, localPath string, progress func(done, total int64)) error {
	out, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer out.Close()
	var off int64
	const chunk = 256 * 1024
	for {
		raw, err := c.Call("get", map[string]any{"path": devicePath, "offset": off, "len": chunk}, 60*time.Second)
		if err != nil {
			return err
		}
		var g struct {
			Data string `json:"data"`
			EOF  bool   `json:"eof"`
			Size int64  `json:"size"`
		}
		if err := json.Unmarshal(raw, &g); err != nil {
			return err
		}
		data, err := base64.StdEncoding.DecodeString(g.Data)
		if err != nil {
			return err
		}
		if _, err := out.Write(data); err != nil {
			return err
		}
		off += int64(len(data))
		if progress != nil {
			progress(off, g.Size)
		}
		if g.EOF || len(data) == 0 {
			break
		}
	}
	return nil
}

// UpdateApp uploads a new app binary to a staging path, then triggers
// app.update (install to inactive slot, activate, restart).
func (c *Client) UpdateApp(localBin, stageDir string) (json.RawMessage, error) {
	dest := stageDir + "/app.upload"
	sum, err := c.PutFile(localBin, dest, 0o755, 0, nil)
	if err != nil {
		return nil, err
	}
	return c.Call("app.update", map[string]any{"src": dest, "sha256": sum}, 30*time.Second)
}

// UpdateAgent uploads a new agent binary and triggers agent.update (write
// inactive agent slot, flip pointer, agent exits for startup.sh to relaunch).
func (c *Client) UpdateAgent(localBin, stageDir string) (json.RawMessage, error) {
	dest := stageDir + "/agent.upload"
	sum, err := c.PutFile(localBin, dest, 0o755, 0, nil)
	if err != nil {
		return nil, err
	}
	return c.Call("agent.update", map[string]any{"src": dest, "sha256": sum}, 30*time.Second)
}
