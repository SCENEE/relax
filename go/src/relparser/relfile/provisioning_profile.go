package relfile

import (
	"bytes"
	"encoding/gob"
	"github.com/DHowett/go-plist"
	"github.com/syndtr/goleveldb/leveldb"
	"io/ioutil"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const (
	// ProvisioningTypeAdHoc : Adhoc type
	ProvisioningTypeAdHoc = "ad-hoc"
	// ProvisioningTypeAppStore : AppStore type
	ProvisioningTypeAppStore = "app-store"
	// ProvisioningTypeDevelopment : Development type
	ProvisioningTypeDevelopment = "development"
	// ProvisioningTypeEnterprise : Enterprise type
	ProvisioningTypeEnterprise = "enterprise"

	// CertificateTypeDeveloper : development
	CertificateTypeDeveloper = "iPhone Developer"
	// CertificateTypeDistribution : distribution
	CertificateTypeDistribution = "iPhone Distribution"
)

// ProvisioningProfile struct
type ProvisioningProfile struct {
	AppIDName             string       `plist:"AppIDName"`
	CreationDate          time.Time    `plist:"CreationDate"`
	DeveloperCertificates [][]byte     `plist:"DeveloperCertificates"`
	Entitlements          Entitlements `plist:"Entitlements"`
	Name                  string       `plist:"Name"`
	ProvisionedDevices    []string     `plist:"ProvisionedDevices,omitempty"`
	ProvisionsAllDevices  bool         `plist:"ProvisionsAllDevices,omitempty"`
	TeamIdentifiers       []string     `plist:"TeamIdentifier"`
	TeamName              string       `plist:"TeamName"`
	TimeToLive            int          `plist:"TimeToLive"`
	UUID                  string       `plist:"UUID"`
	Version               int          `plist:"Version"`
}

// Entitlements : Nested struct is not working in go-plist...
type Entitlements struct {
	GetTaskAllow            bool   `plist:"get-task-allow"`
	ApplicationIdentifier   string `plist:"application-identifier"`
	DeveloperTeamIdentifier string `plist:"com.apple.developer.team-identifier"`
}

// TeamID :
func (p ProvisioningProfile) TeamID() (s string) {
	return p.Entitlements.DeveloperTeamIdentifier
}

// AppID :
func (p ProvisioningProfile) AppID() (s string) {
	return strings.TrimLeft(p.Entitlements.ApplicationIdentifier, p.TeamID()+".")
}

// CertificateType :
func (p ProvisioningProfile) CertificateType() string {
	if p.ProvisioningType() == ProvisioningTypeDevelopment {
		return CertificateTypeDeveloper
	}
	return CertificateTypeDistribution
}

// ProvisioningType :
func (p ProvisioningProfile) ProvisioningType() string {
	if p.ProvisionsAllDevices {
		return ProvisioningTypeEnterprise
	}

	if p.ProvisionedDevices == nil {
		return ProvisioningTypeAppStore
	}

	if p.Entitlements.GetTaskAllow {
		return ProvisioningTypeDevelopment
	}
	return ProvisioningTypeAdHoc
}

// GetIdentity :
func (p ProvisioningProfile) GetIdentity() []string {
	certs := []string{}
	return certs

}

func decodeCMS(path string) string {
	out, err := ioutil.TempFile("", "relax/provisioning_profile")

	if err != nil {
		logger.Fatalf("error: %v", err)
	}

	if _, err = exec.Command("/usr/bin/security", "cms", "-D", "-i", path, "-o", out.Name()).Output(); err != nil {
		logger.Fatalf("error: %v", err)
	}

	return out.Name()
}

func newProvisioningProfile(path string) *ProvisioningProfile {
	file, err := os.Open(path)

	if err != nil {
		logger.Fatalf("error: %v", err)
	}

	decoder := plist.NewDecoder(file)

	pp := ProvisioningProfile{}
	if err := decoder.Decode(&pp); err != nil {
		logger.Fatalf("error: %v", err)
	}

	return &pp
}

// NewEntitlements :
func NewEntitlements(m map[string]interface{}) Entitlements {
	return Entitlements{
		GetTaskAllow:            m["get-task-allow"].(bool),
		ApplicationIdentifier:   m["application-identifier"].(string),
		DeveloperTeamIdentifier: m["com.apple.developer.team-identifier"].(string),
	}
}

// ProvisioningProfileInfo :
type ProvisioningProfileInfo struct {
	Pp   ProvisioningProfile
	Name string
}

func getCacheDBName() string {
	return os.TempDir() + "relax/cachedb"
}

func getCacheDB() (*leveldb.DB, error) {
	path := getCacheDBName()
	db, err := leveldb.OpenFile(path, nil)
	if err != nil {
		// Prevent error<file missing [file=MANIFEST-000000]>
		ClearCache()
		return leveldb.OpenFile(path, nil)
	}
	return db, err
}

// ClearCache :
func ClearCache() error {
	return os.RemoveAll(getCacheDBName())
}

var ppRoot string = os.Getenv("HOME") + "/Library/MobileDevice/Provisioning Profiles"

// FindProvisioningProfile ;
func FindProvisioningProfile(pattern string, team string) []*ProvisioningProfileInfo {
	db, err := getCacheDB()
	if err != nil {
		logger.Fatalf("error: %v", err)
	}
	defer db.Close()

	files, err := ioutil.ReadDir(ppRoot)
	if err != nil {
		logger.Fatalf("error: %v", err)
	}

	s := make(chan bool, 32)
	c := make(chan *ProvisioningProfileInfo, len(files))
	count := 0
	for _, file := range files {
		s <- true

		name := file.Name()

		if false == strings.HasSuffix(name, "mobileprovision") {
			continue
		}

		go func() {
			defer func() { <-s }()
			var (
				info   ProvisioningProfileInfo
				buffer bytes.Buffer
			)
			defer func() { c <- &info }()

			if buf, err := db.Get([]byte(name), nil); err == nil {
				dec := gob.NewDecoder(bytes.NewBuffer(buf))
				if err = dec.Decode(&info); err == nil {
					if _, err = os.Stat(info.Name); err == nil {
						return
					}
				}
			}

			in := ppRoot + "/" + name
			out := decodeCMS(in)
			defer os.Remove(out)
			pp := newProvisioningProfile(out)
			info = ProvisioningProfileInfo{Pp: *pp, Name: in}

			enc := gob.NewEncoder(&buffer)
			if err = enc.Encode(info); err != nil {
				logger.Fatalf("error: %v", err)
			}
			if err = db.Put([]byte(name), buffer.Bytes(), nil); err != nil {
				logger.Fatalf("error: %v", err)
			}
		}()
		count++
	}

	var infos []*ProvisioningProfileInfo

	for i := 0; i < count; i++ {
		info := <-c
		if info == nil {
			continue
		}

		if team != "" && team != info.Pp.TeamID() {
			continue
		}

		if pattern != "" {
			if matched, err := regexp.MatchString(pattern, info.Pp.Name); err != nil || !matched {
				continue
			}
		}
		infos = append(infos, info)
	}

	return infos
}
