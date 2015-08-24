package repository_fetcher

import (
	"fmt"
	"strings"
	"time"

	"github.com/cloudfoundry-incubator/garden-linux/process"
	"github.com/docker/docker/image"
	"github.com/pivotal-golang/lager"
)

type RemoteV1Fetcher struct {
	Graph     Graph
	GraphLock Lock
}

func (fetcher *RemoteV1Fetcher) Fetch(request *FetchRequest) (*FetchResponse, error) {
	request.Logger.Debug("docker-v1-fetch")

	repoData, err := request.Session.GetRepositoryData(request.Path)
	if err != nil {
		return nil, FetchError("GetRepositoryData", request.Endpoint.URL.Host, request.Path, err)
	}

	tagsList, err := request.Session.GetRemoteTags(repoData.Endpoints, request.Path)
	if err != nil {
		return nil, FetchError("GetRemoteTags", request.Endpoint.URL.Host, request.Path, err)
	}

	imgID, ok := tagsList[request.Tag]
	if !ok {
		return nil, FetchError("looking up tag", request.Endpoint.URL.Host, request.Path, fmt.Errorf("unknown tag: %v", request.Tag))
	}

	for _, endpointURL := range repoData.Endpoints {
		request.Logger.Debug("trying", lager.Data{
			"endpoint": endpointURL,
			"image":    imgID,
		})

		var image *dockerImage
		image, err = fetcher.fetchFromEndpoint(request, endpointURL, imgID, request.Logger)

		if err == nil {
			request.Logger.Debug("fetched", lager.Data{
				"endpoint": endpointURL,
				"image":    imgID,
				"env":      image.Env(),
				"volumes":  image.Vols(),
			})

			return &FetchResponse{
				ImageID: imgID,
				Volumes: image.Vols(),
				Env:     image.Env(),
			}, nil
		}

		if err == ErrQuotaExceeded { // no point continuing
			return nil, err
		}
	}

	return nil, FetchError("fetchFromEndPoint", request.Endpoint.URL.Host, request.Path, fmt.Errorf("all endpoints failed: %v", err))
}

func (fetcher *RemoteV1Fetcher) fetchFromEndpoint(request *FetchRequest, endpointURL string, imgID string, logger lager.Logger) (*dockerImage, error) {
	history, err := request.Session.GetRemoteHistory(imgID, endpointURL)
	if err != nil {
		return nil, err
	}

	var allLayers []*dockerLayer
	remainingQuota := request.MaxSize
	for i := len(history) - 1; i >= 0; i-- {
		layer, err := fetcher.fetchLayer(request, endpointURL, history[i], remainingQuota, logger)
		if err != nil {
			return nil, err
		}

		allLayers = append(allLayers, layer)

		remainingQuota = remainingQuota - layer.size
		if remainingQuota < 0 {
			return nil, ErrQuotaExceeded
		}
	}

	return &dockerImage{allLayers}, nil
}

func (fetcher *RemoteV1Fetcher) fetchLayer(request *FetchRequest, endpointURL string, layerID string, remaining int64, logger lager.Logger) (*dockerLayer, error) {
	fetcher.GraphLock.Acquire(layerID)
	defer fetcher.GraphLock.Release(layerID)

	img, err := fetcher.Graph.Get(layerID)
	if err == nil {
		request.Logger.Info("using-cached", lager.Data{
			"layer": layerID,
			"size":  img.Size,
		})

		return &dockerLayer{imgEnv(img, request.Logger), imgVolumes(img), img.Size}, nil
	}

	imgJSON, imgSize, err := request.Session.GetRemoteImageJSON(layerID, endpointURL)
	if err != nil {
		return nil, fmt.Errorf("get remote image JSON: %v", err)
	}

	img, err = image.NewImgJSON(imgJSON)
	if err != nil {
		return nil, fmt.Errorf("new image JSON: %v", err)
	}

	layer, err := request.Session.GetRemoteImageLayer(img.ID, endpointURL, int64(imgSize))
	if err != nil {
		return nil, fmt.Errorf("get remote image layer: %v", err)
	}

	defer layer.Close()

	started := time.Now()

	request.Logger.Info("downloading", lager.Data{
		"layer": layerID,
	})

	err = fetcher.Graph.Register(img, &QuotaedReader{R: layer, N: remaining})
	if err != nil {
		return nil, fmt.Errorf("register: %s", err)
	}

	request.Logger.Info("downloaded", lager.Data{
		"layer": layerID,
		"took":  time.Since(started),
		"vols":  imgVolumes(img),
		"size":  img.Size,
	})

	return &dockerLayer{imgEnv(img, request.Logger), imgVolumes(img), img.Size}, nil
}

func imgEnv(img *image.Image, logger lager.Logger) process.Env {
	if img.Config == nil {
		return process.Env{}
	}

	return filterEnv(img.Config.Env, logger)
}

func imgVolumes(img *image.Image) []string {
	var volumes []string

	if img.Config != nil {
		for volumePath, _ := range img.Config.Volumes {
			volumes = append(volumes, volumePath)
		}
	}

	return volumes
}

func filterEnv(env []string, logger lager.Logger) process.Env {
	var filtered []string
	for _, e := range env {
		segs := strings.SplitN(e, "=", 2)
		if len(segs) != 2 {
			// malformed docker image metadata?
			logger.Info("Unrecognised environment variable", lager.Data{"e": e})
			continue
		}
		filtered = append(filtered, e)
	}

	filteredWithNoDups, err := process.NewEnv(filtered)
	if err != nil {
		logger.Error("Invalid environment", err)
	}
	return filteredWithNoDups
}
