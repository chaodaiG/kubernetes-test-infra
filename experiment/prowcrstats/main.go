package main

import (
	"context"
	"flag"
	"io/fs"
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/test-infra/experiment/prowcrstats/kube"
	prowjobv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
)

const (
	defaultNamespace = "default"
	testpodNamespace = "test-pods"

	cacheFileName   = "cache.yaml"
	cacheProwjobDir = "prowjobs"
)

var (
	defaultCacheDir = path.Join(os.Getenv("HOME"), "data", "prow-cr-cache")
)

type cache struct {
	ID         string                `yaml:"id"`
	Type       prowjobv1.ProwJobType `yaml:"type"`
	StartTime  metav1.Time           `yaml:"start_time"`
	Author     string                `yaml:"author,omitempty"`
	AuthorLink string                `yaml:"author_link,omitempty"`
}

type client struct {
	caches   map[string]cache
	mux      sync.RWMutex
	localDir string
}

func (c *client) exist(id string) bool {
	c.mux.RLock()
	defer c.mux.RUnlock()
	_, ok := c.caches[id]
	return ok
}

func (c *client) add(pj prowjobv1.ProwJob) error {
	if pj.Status.CompletionTime == nil {
		return nil
	}
	if c.exist(pj.Name) {
		return nil
	}
	body, err := yaml.Marshal(pj)
	if err != nil {
		return err
	}
	if err := ioutil.WriteFile(path.Join(c.localDir, cacheProwjobDir, pj.Name), body, 0644); err != nil {
		return err
	}
	c.mux.Lock()
	defer c.mux.Unlock()
	if c.caches == nil {
		c.caches = make(map[string]cache)
	}
	if _, ok := c.caches[pj.Name]; !ok {
		cc := cache{
			ID:        pj.Name,
			Type:      pj.Spec.Type,
			StartTime: pj.Status.StartTime,
		}
		if cc.Type == prowjobv1.PresubmitJob {
			cc.Author = pj.Spec.Refs.Pulls[0].Author
			cc.AuthorLink = pj.Spec.Refs.Pulls[0].AuthorLink
		}
		c.caches[pj.Name] = cc
	}
	return nil
}

func (c *client) save() error {
	c.mux.RLock()
	defer c.mux.RUnlock()
	body, err := yaml.Marshal(c.caches)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(path.Join(c.localDir, cacheFileName), body, 0644)
}

func (c *client) loadFromFile() error {
	g := new(errgroup.Group)
	// Walk through all cached files and restruct
	if err := filepath.Walk(path.Join(c.localDir, cacheProwjobDir), func(path string, info fs.FileInfo, err error) error {
		c.mux.Lock()
		defer c.mux.Unlock()
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		// Concurrent reading of files has a cap, doing it outside of goroutine
		body, err := ioutil.ReadFile(path)
		if err != nil {
			return err
		}
		g.Go(func() error {
			var pj prowjobv1.ProwJob
			if err := yaml.Unmarshal(body, &pj); err != nil {
				return err
			}
			return c.add(pj)
		})
		return nil
	}); err != nil {
		return err
	}

	if err := g.Wait(); err != nil {
		return err
	}
	return c.save()
}

func (c *client) loadFromCache() error {
	c.mux.Lock()
	defer c.mux.Unlock()
	body, err := ioutil.ReadFile(path.Join(c.localDir, cacheFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if c.caches == nil {
		c.caches = make(map[string]cache)
	}

	return yaml.Unmarshal(body, c.caches)
}

func (c *client) analyze() error {
	authors := make(map[string][]cache)
	for _, cc := range c.caches {
		if cc.Type != prowjobv1.PresubmitJob {
			continue
		}
		authors[cc.AuthorLink] = append(authors[cc.AuthorLink], cc)
	}

	var jobsCount int
	for _, pjs := range authors {
		jobsCount += len(pjs)
	}
	log.Printf("Authors: %d, jobs: %d", len(authors), jobsCount)
	return nil
}

func main() {
	clusterContext := flag.String("cluster", "gke_gob-prow_us-west1-a_prow", "The context of cluster to use for test")
	cacheDir := flag.String("cache-dir", defaultCacheDir, "")
	cacheOnly := flag.Bool("cache-only", false, "")
	skipUpdating := flag.Bool("skip-updating", false, "")
	flag.Parse()

	c := &client{
		localDir: *cacheDir,
	}
	if *cacheOnly {
		log.Print("Load from cache.")
		if err := c.loadFromCache(); err != nil {
			log.Fatal(err)
		}
	} else {
		log.Print("Load from local files.")
		if err := c.loadFromFile(); err != nil {
			log.Fatal(err)
		}
	}

	if !*skipUpdating {
		log.Print("Listing prow jobs from prow cluster.")
		kubeClient, err := kube.NewClients("", *clusterContext)
		if err != nil {
			log.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		pjs, err := kube.ListProwjobs(ctx, kubeClient, defaultNamespace)
		if err != nil {
			log.Fatal(err)
		}
		log.Print("Listed prowjobs: ", len(pjs.Items))

		// Process pjs
		for _, pj := range pjs.Items {
			if err := c.add(pj); err != nil {
				log.Print(err)
			}
		}
		if err := c.save(); err != nil {
			log.Fatal(err)
		}
		log.Print("Hello!")
	}

	log.Print("Analyzing prowjobs: ", len(c.caches))
	c.analyze()
}
