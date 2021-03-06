package main

import (
	"fmt"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	"golang.org/x/net/context"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
	"io/ioutil"
)

var logPrintf = log.Printf
var dockerClient = client.NewClient
var serviceLastCreatedAt time.Time

type Service struct {
	Host                  string
	NotifCreateServiceUrl string
	NotifRemoveServiceUrl string
	Services              map[string]bool
}

type Servicer interface {
	GetServices() ([]swarm.Service, error)
	GetNewServices(services []swarm.Service) ([]swarm.Service, error)
	NotifyServicesCreate(services []swarm.Service, retries, interval int) error
	NotifyServicesRemove(services []string, retries, interval int) error
}

func (m *Service) GetServices() ([]swarm.Service, error) {
	defaultHeaders := map[string]string{"User-Agent": "engine-api-cli-1.0"}
	dc, err := dockerClient(m.Host, "v1.22", nil, defaultHeaders)

	if err != nil {
		return []swarm.Service{}, err
	}

	services, err := dc.ServiceList(context.Background(), types.ServiceListOptions{})
	if err != nil {
		return []swarm.Service{}, err
	}

	return services, nil
}

func (m *Service) GetNewServices(services []swarm.Service) ([]swarm.Service, error) {
	newServices := []swarm.Service{}
	tmpCreatedAt := serviceLastCreatedAt
	for _, s := range services {
		if tmpCreatedAt.Nanosecond() == 0 || s.Meta.CreatedAt.After(tmpCreatedAt) {
			if _, ok := s.Spec.Labels["com.df.notify"]; ok {
				newServices = append(newServices, s)
				m.Services[s.Spec.Name] = true
				if serviceLastCreatedAt.Before(s.Meta.CreatedAt) {
					serviceLastCreatedAt = s.Meta.CreatedAt
				}
			}
		}
	}
	return newServices, nil
}

func (m *Service) GetRemovedServices(services []swarm.Service) []string {
	tmpMap := make(map[string]bool)
	for k, _ := range m.Services {
		tmpMap[k] = true
	}
	for _, v := range services {
		if _, ok := m.Services[v.Spec.Name]; ok {
			delete(tmpMap, v.Spec.Name)
		}
	}
	rs := []string{}
	for k, _ := range tmpMap {
		rs = append(rs, k)
	}
	return rs
}

func (m *Service) NotifyServicesCreate(services []swarm.Service, retries, interval int) error {
	errs := []error{}
	for _, s := range services {
		fullUrl := fmt.Sprintf("%s?serviceName=%s", m.NotifCreateServiceUrl, s.Spec.Name)
		if _, ok := s.Spec.Labels["com.df.notify"]; ok {
			for k, v := range s.Spec.Labels {
				if strings.HasPrefix(k, "com.df") && k != "com.df.notify" {
					fullUrl = fmt.Sprintf("%s&%s=%s", fullUrl, strings.TrimPrefix(k, "com.df."), v)
				}
			}
			logPrintf("Sending service created notification to %s", fullUrl)
			for i := 1; i <= retries; i++ {
				resp, err := http.Get(fullUrl)
				if err == nil && resp.StatusCode == http.StatusOK {
					break
				} else if i < retries {
					if interval > 0 {
						t := time.NewTicker(time.Second * time.Duration(interval))
						<-t.C
					}
				} else {
					if err != nil {
						logPrintf("ERROR: %s", err.Error())
						errs = append(errs, err)
					} else if resp.StatusCode != http.StatusOK {
						body, _ := ioutil.ReadAll(resp.Body)
						msg := fmt.Errorf("Request %s returned status code %d\n%s", fullUrl, resp.StatusCode, string(body[:]))
						logPrintf("ERROR: %s", msg)
						errs = append(errs, msg)
					}
				}
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("At least one request produced errors. Please consult logs for more details.")
	}
	return nil
}

func (m *Service) NotifyServicesRemove(services []string, retries, interval int) error {
	errs := []error{}
	for _, v := range services {
		fullUrl := fmt.Sprintf("%s?serviceName=%s", m.NotifRemoveServiceUrl, v)
		logPrintf("Sending service removed notification to %s", fullUrl)
		for i := 1; i <= retries; i++ {
			resp, err := http.Get(fullUrl)
			if err == nil && resp.StatusCode == http.StatusOK {
				delete(m.Services, v)
				break
			} else if i < retries {
				if interval > 0 {
					t := time.NewTicker(time.Second * time.Duration(interval))
					<-t.C
				}
			} else {
				if err != nil {
					logPrintf("ERROR: %s", err.Error())
					errs = append(errs, err)
				} else if resp.StatusCode != http.StatusOK {
					msg := fmt.Errorf("Request %s returned status code %d", fullUrl, resp.StatusCode)
					logPrintf("ERROR: %s", msg)
					errs = append(errs, msg)
				}
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("At least one request produced errors. Please consult logs for more details.")
	}
	return nil
}

func NewService(host, notifCreateServiceUrl, notifRemoveServiceUrl string) *Service {
	return &Service{
		Host: host,
		NotifCreateServiceUrl: notifCreateServiceUrl,
		NotifRemoveServiceUrl: notifRemoveServiceUrl,
		Services:              make(map[string]bool),
	}
}

func NewServiceFromEnv() *Service {
	host := "unix:///var/run/docker.sock"
	if len(os.Getenv("DF_DOCKER_HOST")) > 0 {
		host = os.Getenv("DF_DOCKER_HOST")
	}
	notifCreateServiceUrl := os.Getenv("DF_NOTIF_CREATE_SERVICE_URL")
	if len(notifCreateServiceUrl) == 0 {
		notifCreateServiceUrl = os.Getenv("DF_NOTIFICATION_URL")
	}
	notifRemoveServiceUrl := os.Getenv("DF_NOTIF_REMOVE_SERVICE_URL")
	if len(notifRemoveServiceUrl) == 0 {
		notifRemoveServiceUrl = os.Getenv("DF_NOTIFICATION_URL")
	}
	return &Service{
		Host: host,
		NotifCreateServiceUrl: notifCreateServiceUrl,
		NotifRemoveServiceUrl: notifRemoveServiceUrl,
		Services:              make(map[string]bool),
	}
}
