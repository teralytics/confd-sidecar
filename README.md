# confd-sidecar: bring confd and your container app together

This package is a companion to [confd](https://github.com/kelseyhightower/confd) that enhances it in two ways:

1. It autoconfigures confd upon start, based purely on environment variables.  Perfect for running confd as a Marathon application or other containerization framework.
  * This frees you from having to deploy confd template or configuration files on your machines at runtime, or inside your containers during their build process.
2. It runs your service as a child process, while watching confd.  When confd rebuilds your service's configuration files, it sends SIGHUP to your service.
  * This frees you from having to write potentially-fragile `reload_cmd` stanzas in your confd TOML configuration files.

In a real sense, this program brings the equivalent of Kubernetes' dynamically-reconfigurable ConfigMaps to the world of Marathon and Docker Swarm.

## What's it ideal for?

* It'll help you make very lean containers that ship little to no operating system userspace — not even GNU coreutils.
* It'll ensure that your containers can be dynamically reconfigured, and your services reloaded, without external helpers.
* Together with confd, it will deliver the ability to dynamically reconfigure services that weren't built for container-era dynamic reconfiguration, and still use configuration files.
* It can, of course, reconfigure system services running on your hosts as well.

All it needs from your service, is that it accept SIGHUP as a signal to reload its configuration.

## How does it operate?

All of the magic happens at runtime.  At build time, all you need to do is put confd and confd-sidecar inside your container image.

* You set some environment variables.
  * You will most likely do this through your distributed containerization system, such as Marathon or Kubernetes. 
* You then start confd-sidecar, giving it as its command line the program name and the arguments of the program you want it to supervise.
  * The most likely scenario is that your program will be in a container, which also contains both confd and confd-sidecar.
* conf-sidecar starts, preconfiguring confd for you.
* After confd has started successfully, confd-sidecar starts your program exactly as specified in the command line.
* When confd announces that it has reconfigured its managed configuration files, confd-sidecar waits one second, then sends a SIGHUP signal to the supervised running program.
* Your supervised program reloads its configuration.

Some things are best shown by example.

(You can test the following example on the command line, provided that you have the Prometheus master, confd, and confd-sidecar, all built and deployed in your `$PATH`.  In a container scenario, these three would be built inside your container — see below for example instructions.)

```bash
# Let's define some variables now.
export CONFD_CONFDIR=/tmp/confd
export CONFD_BACKEND=consul
export CONFD_NODE=localhost:8500
export CONFD_CONFDFILE_1='prometheus.toml
[template]
src = "prometheus.yml.tmpl"
dest = "/tmp/prometheus/prometheus.yml"
keys = ["/prometheus/config/nodes"]
'
export CONFD_TEMPLATE_1='prometheus.yml.tmpl
global:
    scrape_interval:     15s
    evaluation_interval: 15s
scrape_configs:
    - job_name: "prometheus"
    static_configs:
        - targets: ["localhost:9090"]
    - job_name: "node"
    static_configs:
        - targets: {{ getv "/prometheus/config/nodes" }}
'

# Now let's run prometheus under confd-sidecar.
confd-sidecar prometheus -config.file=/tmp/prometheus/prometheus.yml

# Here is some example (actual) logging output of what happens now:
#
#   2017/03/10 14:23:46 supervisor[1]: main program and arguments to be run: [prometheus -config.file=/tmp/prometheus/prometheus.yml]
#   2017/03/10 14:23:46 supervisor[1]: creating / updating confd conf.d file prometheus.toml
#   2017/03/10 14:23:46 supervisor[1]: creating / updating confd template file prometheus.yml.tmpl
#   2017/03/10 14:23:46 supervisor[1]: confd started — waiting one second to start main program
#   2017-03-10T14:23:46Z c2869ccbb448 /bin/confd[12]: INFO Backend set to consul
#   2017-03-10T14:23:46Z c2869ccbb448 /bin/confd[12]: INFO Starting confd
#   2017-03-10T14:23:46Z c2869ccbb448 /bin/confd[12]: INFO Backend nodes set to localhost:8500
#   2017-03-10T14:23:46Z c2869ccbb448 /bin/confd[12]: INFO /tmp/prometheus/prometheus.yml has md5sum 8e5e2dbcad08bd5acb9770799400f515 should be ba04dccd5a1fe4eb28485b38cbf53f1b
#   2017-03-10T14:23:46Z c2869ccbb448 /bin/confd[12]: INFO Target config /tmp/prometheus/prometheus.yml out of sync
#   2017-03-10T14:23:46Z c2869ccbb448 /bin/confd[12]: INFO Target config /tmp/prometheus/prometheus.yml has been updated
#   2017/03/10 14:23:46 supervisor[1]: confd has updated a config file — waiting one second to start main program
#   2017/03/10 14:23:47 supervisor[1]: starting main program now
#   time="2017-03-10T14:23:47Z" level=info msg="Starting prometheus (version=1.5.2, branch=master, revision=bd1182d29f462c39544f94cc822830e1c64cf55b)" source="main.go:75" 
#   time="2017-03-10T14:23:47Z" level=info msg="Build context (go=go1.7.5, user=root@1a01c5f68840, date=20170210-16:23:28)" source="main.go:76" 
#   time="2017-03-10T14:23:47Z" level=info msg="Loading configuration file /tmp/prometheus/prometheus.yml" source="main.go:248" 
#   time="2017-03-10T14:23:48Z" level=info msg="Loading series map and head chunks..." source="storage.go:373" 
#   time="2017-03-10T14:23:48Z" level=info msg="0 series loaded." source="storage.go:378" 
#   time="2017-03-10T14:23:48Z" level=info msg="Starting target manager..." source="targetmanager.go:61" 
#   time="2017-03-10T14:23:48Z" level=info msg="Listening on :9090" source="web.go:259"
#
# Now two minutes have passed.  The key in Consul "/prometheus/config/nodes" has been updated.  confd kicks in.
#
#   2017-03-10T14:25:51Z c2869ccbb448 /bin/confd[12]: INFO /tmp/prometheus/prometheus.yml has md5sum ba04dccd5a1fe4eb28485b38cbf53f1b should be c243637d3781c7b06b7a7c3fe713f59a
#   2017-03-10T14:25:51Z c2869ccbb448 /bin/confd[12]: INFO Target config /tmp/prometheus/prometheus.yml out of sync
#   2017-03-10T14:25:51Z c2869ccbb448 /bin/confd[12]: INFO Target config /tmp/prometheus/prometheus.yml has been updated
#   2017/03/10 14:25:51 supervisor[1]: confd has updated a config file — waiting one second to reload main program
#   2017/03/10 14:25:52 supervisor[1]: reloading main program now
#   time="2017-03-10T14:25:52Z" level=info msg="Loading configuration file /tmp/prometheus/prometheus.yml" source="main.go:248" 
#
# As you can see, the SIGHUP sent to prometheus made it reload its config file instantly.
```

## How do I integrate this into my container service?

Two steps:

1. Build confd and confd-sidecar into your container image.
2. Set the proper variables up in your service definition that uses said container image.

### Building your container image

Let's suppose we want to bundle the Prometheus master container up together with confd and confd-sidecar.

Let's first build confd and confd-sidecar.

```bash
export GOPATH="$PWD"/go
mkdir -p "$GOPATH"
CGO_ENABLED=0 go get -ldflags '-extld ld -extldflags -static' github.com/kelseyhightower/confd
go get github.com/teralytics/confd-sidecar
```

Now let's bundle it into a new container image.

```bash
mkdir -p prometheus/bin
mv "$GOPATH"/bin/* prometheus/bin/

cat > Dockerfile << EOFEOF
FROM prom/prometheus:latest

COPY bin/confd /bin/confd
COPY bin/confd-sidecar /bin/confd-sidecar

ENTRYPOINT ["/bin/confd-sidecar"]

CMD ["prometheus", "-config.file=/etc/prometheus/prometheus.yml", "-storage.local.path=/prometheus", "-web.console.libraries=/usr/share/prometheus/console_libraries", "-web.console.templates=/usr/share/prometheus/consoles"]
EOFEOF

docker build prometheus

# ...
# ...logs omitted...
# ...

docker tag <image hash> yourregistry.host/prometheus:latest
docker push yourregistry.host/prometheus:latest
```

Now you have a container that has everything you need to get started.

### Setting the proper variables up at runtime

Here is a minimal sample JSON file for Marathon that would run the above container with confd-sidecar functionality.  It will pull the Prometheus configuration file from the Consul K/V key `/prometheus/configfile`, and deploy it inside the container for you.  The `args` data is passed as arguments to `confd-sidecar` (defined as the entry point for the container).

(Note that you will have to figure out a persistent volume solution for the `/prometheus` volume that stores the collected Prometheus data.)

```json
{
  "id": "/prometheus-master",
  "cmd": null,
  "cpus": 0.5,
  "mem": 4096,
  "disk": 0,
  "instances": 1,
  "acceptedResourceRoles": [
    "*"
  ],
  "container": {
    "type": "DOCKER",
    "volumes": [],
    "docker": {
      "image": "yourregistry.host/prometheus:latest",
      "network": "BRIDGE",
      "portMappings": [
        {
          "containerPort": 9090,
          "hostPort": 0,
          "protocol": "tcp",
          "labels": {}
        }
      ],
      "privileged": false,
      "parameters": [],
      "forcePullImage": true
    }
  },
  "env": {
    "CONFD_TEMPLATE_1": "prometheus.yml.tmpl\n{{ getv \"/prometheus/configfile\" }}\n",
    "CONFD_CONFDFILE_1": "prometheus.toml\n[template]\nsrc = \"prometheus.yml.tmpl\"\ndest = \"/etc/prometheus/prometheus.yml\"\nkeys = [\"/prometheus/configfile\"]\n",
    "CONFD_PATH": "/bin/confd",
    "CONFD_BACKEND": "consul",
    "CONFD_NODE": "localhost:8500",
    "CONFD_CONFDIR": "/etc/confd"
  },
  "healthChecks": [
    {
      "path": "/api/v1/query?query=up",
      "protocol": "HTTP",
      "portIndex": 0,
      "gracePeriodSeconds": 300,
      "intervalSeconds": 60,
      "timeoutSeconds": 20,
      "maxConsecutiveFailures": 3,
      "ignoreHttp1xx": false
    }
  ],
  "labels": {},
  "args": [
    "prometheus",
    "-config.file=/etc/prometheus/prometheus.yml",
    "-storage.local.path=/prometheus",
    "-web.console.libraries=/usr/share/prometheus/console_libraries",
    "-web.console.templates=/usr/share/prometheus/consoles",
  ]
}
```

## Building instructions

The recommended way of building it is via the traditional `go get`:

```bash
export GOPATH="$PWD"/go
mkdir -p "$GOPATH"
go get github.com/teralytics/confd-sidecar
```

You'll need confd too.  Note that, for many container scenarios, confd will have to be built statically, else your container may fail to start the application altogether with a *No such file or directory* error.  Here's how to work around that problem:

```bash
export GOPATH="$PWD"/go
mkdir -p "$GOPATH"
CGO_ENABLED=0 go get -ldflags '-extld ld -extldflags -static' github.com/kelseyhightower/confd
```

## Help

Run `confd-sidecar` with no parameters to get information.

## License

This program is distributed under the [Apache 2.0](LICENSE) license.
