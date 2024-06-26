package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	sp "github.com/linkerd/linkerd2/controller/gen/apis/serviceprofile/v1alpha2"
	pkgcmd "github.com/linkerd/linkerd2/pkg/cmd"
	"github.com/linkerd/linkerd2/pkg/healthcheck"
	"github.com/linkerd/linkerd2/pkg/k8s"
	"github.com/linkerd/linkerd2/pkg/profiles"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/yaml"
)

type profileOptions struct {
	name          string
	namespace     string
	template      bool
	openAPI       string
	proto         string
	ignoreCluster bool
	output        string
}

func newProfileOptions() *profileOptions {
	return &profileOptions{
		name:          "",
		template:      false,
		openAPI:       "",
		proto:         "",
		ignoreCluster: false,
		output:        "yaml",
	}
}

func (options *profileOptions) validate() error {
	outputs := 0
	if options.template {
		outputs++
	}
	if options.openAPI != "" {
		outputs++
	}
	if options.proto != "" {
		outputs++
	}
	if outputs != 1 {
		return errors.New("You must specify exactly one of --template or --open-api or --proto")
	}

	// a DNS-1035 label must consist of lower case alphanumeric characters or '-',
	// start with an alphabetic character, and end with an alphanumeric character
	if errs := validation.IsDNS1035Label(options.name); len(errs) != 0 {
		return fmt.Errorf("invalid service %q: %v", options.name, errs)
	}

	// a DNS-1123 label must consist of lower case alphanumeric characters or '-',
	// and must start and end with an alphanumeric character
	if errs := validation.IsDNS1123Label(options.namespace); len(errs) != 0 {
		return fmt.Errorf("invalid namespace %q: %v", options.namespace, errs)
	}

	return nil
}

// newCmdProfile creates a new cobra command for the Profile subcommand which
// generates Linkerd service profiles.
func newCmdProfile() *cobra.Command {
	options := newProfileOptions()

	cmd := &cobra.Command{
		Use:   "profile [flags] (--template | --open-api file | --proto file) (SERVICE)",
		Short: "Output service profile config for Kubernetes",
		Long:  "Output service profile config for Kubernetes.",
		Example: `  # Output a basic template to apply after modification.
  linkerd profile -n emojivoto --template web-svc

  # Generate a profile from an OpenAPI specification.
  linkerd profile -n emojivoto --open-api web-svc.swagger web-svc

  # Generate a profile from a protobuf definition.
  linkerd profile -n emojivoto --proto Voting.proto vote-svc
`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if options.namespace == "" {
				options.namespace = pkgcmd.GetDefaultNamespace(kubeconfigPath, kubeContext)
			}
			options.name = args[0]
			clusterDomain := defaultClusterDomain

			err := options.validate()
			if err != nil {
				return err
			}
			// performs an online profile generation and access-check to k8s cluster to extract
			// clusterDomain from linkerd configuration
			if !options.ignoreCluster {
				var err error
				k8sAPI, err := k8s.NewAPI(kubeconfigPath, kubeContext, impersonate, impersonateGroup, 0)

				if err != nil {
					return err
				}

				_, values, err := healthcheck.FetchCurrentConfiguration(cmd.Context(), k8sAPI, controlPlaneNamespace)
				if err != nil {
					return err
				}

				if cd := values.ClusterDomain; cd != "" {
					clusterDomain = cd
				}
			}

			var profile *sp.ServiceProfile
			if options.template {
				return profiles.RenderProfileTemplate(options.namespace, options.name, clusterDomain, os.Stdout, options.output)
			} else if options.openAPI != "" {
				profile, err = profiles.RenderOpenAPI(options.openAPI, options.namespace, options.name, clusterDomain)
			} else if options.proto != "" {
				profile, err = profiles.RenderProto(options.proto, options.namespace, options.name, clusterDomain)
			} else {
				return errors.New("one of --template, --open-api, or --proto must be specified")
			}
			if err != nil {
				return err
			}

			return writeProfile(profile, os.Stdout, options.output)
		},
	}

	cmd.PersistentFlags().BoolVar(&options.template, "template", options.template, "Output a service profile template")
	cmd.PersistentFlags().StringVar(&options.openAPI, "open-api", options.openAPI, "Output a service profile based on the given OpenAPI spec file")
	cmd.PersistentFlags().StringVarP(&options.namespace, "namespace", "n", options.namespace, "Namespace of the service")
	cmd.PersistentFlags().StringVar(&options.proto, "proto", options.proto, "Output a service profile based on the given Protobuf spec file")
	cmd.PersistentFlags().BoolVar(&options.ignoreCluster, "ignore-cluster", options.ignoreCluster, "Output a service profile through offline generation")
	cmd.PersistentFlags().StringVarP(&options.output, "output", "o", options.output, "Output format. One of: yaml, json")
	return cmd
}

func writeProfile(profile *sp.ServiceProfile, w io.Writer, format string) error {
	var output []byte
	var err error
	if format == yamlOutput {
		output, err = yaml.Marshal(profile)
	} else if format == jsonOutput {
		output, err = json.Marshal(profile)
	} else {
		return fmt.Errorf("unknown output format: %s", format)
	}
	if err != nil {
		return fmt.Errorf("Error writing Service Profile: %w", err)
	}
	_, err = w.Write(output)
	return err
}
