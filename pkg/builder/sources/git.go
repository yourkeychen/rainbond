// RAINBOND, Application Management Platform
// Copyright (C) 2014-2017 Goodrain Co., Ltd.

// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. For any non-GPL usage of Rainbond,
// one or multiple Commercial Licenses authorized by Goodrain Co., Ltd.
// must be obtained first.

// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.

// You should have received a copy of the GNU General Public License
// along with this program. If not, see <http://www.gnu.org/licenses/>.

package sources

import (
	"bufio"
	"context"
	"crypto/sha1"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"strings"
	"time"

	"github.com/twinj/uuid"

	"github.com/Sirupsen/logrus"

	"gopkg.in/src-d/go-git.v4/plumbing/transport/http"

	"github.com/goodrain/rainbond/pkg/event"
	"github.com/goodrain/rainbond/pkg/util"
	git "gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing"
	"gopkg.in/src-d/go-git.v4/plumbing/protocol/packp/sideband"
	"gopkg.in/src-d/go-git.v4/plumbing/transport"
	"gopkg.in/src-d/go-git.v4/plumbing/transport/ssh"
)

//CodeSourceInfo 代码源信息
type CodeSourceInfo struct {
	ServerType    string `json:"server_type"`
	RepositoryURL string `json:"repository_url"`
	Branch        string `json:"branch"`
	User          string `json:"user"`
	Password      string `json:"password"`
	//避免项目之间冲突，代码缓存目录提高到租户
	TenantID string `json:"tenant_id"`
}

//GetCodeCacheDir 获取代码缓存目录
func (c CodeSourceInfo) GetCodeCacheDir() string {
	cacheDir := os.Getenv("CACHE_DIR")
	if cacheDir == "" {
		cacheDir = "/cache"
	}
	h := sha1.New()
	h.Write([]byte(c.RepositoryURL))
	bs := h.Sum(nil)
	bsStr := fmt.Sprintf("%x", bs)
	logrus.Debugf("git path is %s", path.Join(cacheDir, "build", c.TenantID, bsStr))
	return path.Join(cacheDir, "build", c.TenantID, bsStr)
}

//GetCodeSourceDir 获取代码下载目录
func (c CodeSourceInfo) GetCodeSourceDir() string {
	return GetCodeSourceDir(c.RepositoryURL, c.TenantID)
}

//GetCodeSourceDir 获取源码下载目录
func GetCodeSourceDir(RepositoryURL, tenantID string) string {
	sourceDir := os.Getenv("SOURCE_DIR")
	if sourceDir == "" {
		sourceDir = "/grdata/source"
	}
	h := sha1.New()
	h.Write([]byte(RepositoryURL))
	bs := h.Sum(nil)
	bsStr := fmt.Sprintf("%x", bs)
	return path.Join(sourceDir, "build", tenantID, bsStr)
}

//CheckFileExist CheckFileExist
func CheckFileExist(path string) bool {
	_, err := os.Stat(path)
	if err != nil {
		if os.IsExist(err) {
			return true
		}
		return false
	}
	return true
}

//RemoveDir RemoveDir
func RemoveDir(path string) error {
	if path == "/" {
		return fmt.Errorf("remove wrong dir")
	}
	return os.RemoveAll(path)
}

//GitClone git clone code
func GitClone(csi CodeSourceInfo, sourceDir string, logger event.Logger, timeout int) (*git.Repository, error) {
	if logger != nil {
		//进度信息
		logger.Info(fmt.Sprintf("开始从Git源(%s)获取代码", csi.RepositoryURL), map[string]string{"step": "clone_code"})
	}
	ep, err := transport.NewEndpoint(csi.RepositoryURL)
	if err != nil {
		return nil, err
	}
	//最少一分钟
	if timeout < 1 {
		timeout = 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*time.Duration(timeout))
	defer cancel()
	stop := make(chan struct{})
	progress := createProgress(ctx, logger, stop)
	opts := &git.CloneOptions{
		URL:               csi.RepositoryURL,
		Progress:          progress,
		SingleBranch:      false,
		RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
	}
	if csi.Branch != "" {
		opts.ReferenceName = plumbing.ReferenceName(fmt.Sprintf("refs/heads/%s", csi.Branch))
	}
	var rs *git.Repository
	if ep.Protocol == "ssh" {
		publichFile := GetPrivateFile()
		sshAuth, auerr := ssh.NewPublicKeysFromFile("git", publichFile, "")
		if auerr != nil {
			if logger != nil {
				logger.Error(fmt.Sprintf("创建PublicKeys错误"), map[string]string{"step": "callback", "status": "failure"})
			}
			return nil, auerr
		}
		opts.Auth = sshAuth
		rs, err = git.PlainCloneContext(ctx, sourceDir, false, opts)
	} else {
		if csi.User != "" && csi.Password != "" {
			httpAuth := &http.BasicAuth{
				Username: csi.User,
				Password: csi.Password,
			}
			opts.Auth = httpAuth
		}
		rs, err = git.PlainCloneContext(ctx, sourceDir, false, opts)
	}
	if err != nil {
		if reerr := os.RemoveAll(sourceDir); reerr != nil {
			if logger != nil {
				logger.Error(fmt.Sprintf("拉取代码发生错误删除代码目录失败。"), map[string]string{"step": "callback", "status": "failure"})
			}
		}
		if err == transport.ErrAuthenticationRequired {
			if logger != nil {
				logger.Error(fmt.Sprintf("拉取代码发生错误，代码源需要授权访问。"), map[string]string{"step": "callback", "status": "failure"})
			}
			return rs, err
		}
		if err == transport.ErrAuthorizationFailed {
			if logger != nil {
				logger.Error(fmt.Sprintf("拉取代码发生错误，代码源鉴权失败。"), map[string]string{"step": "callback", "status": "failure"})
			}
			return rs, err
		}
		if err == transport.ErrRepositoryNotFound {
			if logger != nil {
				logger.Error(fmt.Sprintf("拉取代码发生错误，仓库不存在。"), map[string]string{"step": "callback", "status": "failure"})
			}
			return rs, err
		}
		if err == transport.ErrEmptyRemoteRepository {
			if logger != nil {
				logger.Error(fmt.Sprintf("拉取代码发生错误，远程仓库为空。"), map[string]string{"step": "callback", "status": "failure"})
			}
			return rs, err
		}
		if err == plumbing.ErrReferenceNotFound {
			if logger != nil {
				logger.Error(fmt.Sprintf("代码分支(%s)不存在。", csi.Branch), map[string]string{"step": "callback", "status": "failure"})
			}
			return rs, fmt.Errorf("branch %s is not exist", csi.Branch)
		}
		if strings.Contains(err.Error(), "ssh: unable to authenticate") {
			if logger != nil {
				logger.Error(fmt.Sprintf("远程代码库需要配置SSH Key。"), map[string]string{"step": "callback", "status": "failure"})
			}
			return rs, err
		}
	}
	return rs, err
}
func retryAuth(ep *transport.Endpoint, csi CodeSourceInfo) (transport.AuthMethod, error) {
	switch ep.Protocol {
	case "ssh":
		home, _ := Home()
		sshAuth, err := ssh.NewPublicKeysFromFile("git", path.Join(home, "/.ssh/id_rsa"), "")
		if err != nil {
			return nil, err
		}
		return sshAuth, nil
	case "http", "https":
		//return http.NewBasicAuth(csi.User, csi.Password), nil
	}
	return nil, nil
}

//GitPull git pull code
func GitPull(csi CodeSourceInfo, sourceDir string, logger event.Logger, timeout int) (*git.Repository, error) {
	var rs *git.Repository

	return rs, nil
}

//GetPrivateFile 获取私钥文件地址
func GetPrivateFile() string {
	home, _ := Home()
	if home == "" {
		home = "/root"
	}
	if ok, _ := util.FileExists(path.Join(home, "/.ssh/builder_rsa")); ok {
		return path.Join(home, "/.ssh/builder_rsa")
	}
	return path.Join(home, "/.ssh/id_rsa")
}

//GetPublicKey 获取公钥
func GetPublicKey() string {
	home, _ := Home()
	if home == "" {
		home = "/root"
	}
	if ok, _ := util.FileExists(path.Join(home, "/.ssh/builder_rsa.pub")); ok {
		body, _ := ioutil.ReadFile(path.Join(home, "/.ssh/builder_rsa.pub"))
		return string(body)
	}
	body, _ := ioutil.ReadFile(path.Join(home, "/.ssh/id_rsa.pub"))
	return string(body)
}

//createProgress create git log progress
func createProgress(ctx context.Context, logger event.Logger, stop chan struct{}) sideband.Progress {
	if logger == nil {
		return os.Stdout
	}
	name := "/tmp/" + uuid.NewV4().String()
	bufferfile, err := os.OpenFile(name, os.O_RDWR|os.O_WRONLY, 755)
	if err != nil {
		return os.Stdout
	}
	var reader = bufio.NewReader(bufferfile)
	go func() {
		defer func() {
			bufferfile.Close()
			os.RemoveAll(name)
		}()
		defer close(stop)
		for {
			select {
			case <-ctx.Done():
				return
			default:
				line, _, err := reader.ReadLine()
				if err != nil {
					fmt.Println("err", err.Error())
					return
				}
				logger.Debug(string(line), map[string]string{"step": "code_progress"})
			}
		}
	}()
	return bufferfile
}
