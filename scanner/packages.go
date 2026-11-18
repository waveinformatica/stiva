package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	cdx "github.com/CycloneDX/cyclonedx-go"

	ftypes "github.com/aquasecurity/trivy/pkg/fanal/types"
	"github.com/aquasecurity/trivy/pkg/sbom/cyclonedx"
	"github.com/aquasecurity/trivy/pkg/types"
)

// Packages are kept once in the registry (docs/scanning.md). A package is
// identified by its context, what Trivy's match depends on besides the package
// itself, and by the package without what depends on where it sits in an
// image.

// pkgContext is the context of a package: the OS for OS packages (Trivy
// matches them against that release's advisories), the ecosystem for
// libraries.
type pkgContext struct {
	Class types.ResultClass `json:"class"`
	Type  ftypes.TargetType `json:"type"`
	OS    *ftypes.OS        `json:"os,omitempty"`
}

func contextOf(rep *types.Report, res types.Result) (pkgContext, error) {
	switch res.Class {
	case types.ClassOSPkg:
		if rep.Metadata.OS == nil {
			return pkgContext{}, fmt.Errorf("%s: OS packages without an OS", res.Target)
		}
		os := *rep.Metadata.OS
		// End of life is decided at match time from the database, not a
		// property of the package.
		os.Eosl = false
		return pkgContext{Class: res.Class, Type: res.Type, OS: &os}, nil
	case types.ClassLangPkg:
		return pkgContext{Class: res.Class, Type: res.Type}, nil
	default:
		return pkgContext{}, fmt.Errorf("%s: packages of class %q", res.Target, res.Class)
	}
}

// identity is the package without its location in the image: the layer, the
// file, the dependency graph and the identifiers Trivy derives from them.
// Everything the match reads stays (name, versions, source package, Red Hat
// build info and modularity label, repository).
func identity(p ftypes.Package) ftypes.Package {
	p.Identifier.UID = ""
	p.Identifier.BOMRef = ""
	p.DependsOn = nil
	p.Relationship = ftypes.RelationshipUnknown
	p.Indirect = false
	p.Layer = ftypes.Layer{}
	p.FilePath = ""
	p.Digest = ""
	p.Locations = nil
	p.InstalledFiles = nil
	return p
}

// imagePackages turns an image report (made with --list-all-pkgs) into the
// packages, their vulnerabilities and their occurrences in the image.
func imagePackages(rep *types.Report) ([]scanPackage, []occurrence, error) {
	var pkgs []scanPackage
	index := map[string]int{} // canonical {context, package} -> pkgs index
	type occKey struct {
		target string
		pkg    int
	}
	counts := map[occKey]int{}
	var order []occKey

	for _, res := range rep.Results {
		if len(res.Packages) == 0 {
			continue
		}
		pctx, err := contextOf(rep, res)
		if err != nil {
			return nil, nil, err
		}
		ctxJSON, err := json.Marshal(pctx)
		if err != nil {
			return nil, nil, err
		}
		// Vulnerabilities of this result, by the UID of the package they
		// belong to.
		byUID := map[string][]vulnerability{}
		for _, v := range res.Vulnerabilities {
			sev, err := severity(v.Severity)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %w", v.VulnerabilityID, err)
			}
			if v.PkgIdentifier.UID == "" {
				return nil, nil, fmt.Errorf("%s in %s: no package identifier", v.VulnerabilityID, res.Target)
			}
			byUID[v.PkgIdentifier.UID] = append(byUID[v.PkgIdentifier.UID], vulnerability{
				ID: v.VulnerabilityID, Severity: sev, Fixed: v.FixedVersion, Title: v.Title,
			})
		}
		matched := 0
		for _, p := range res.Packages {
			pkgJSON, err := json.Marshal(identity(p))
			if err != nil {
				return nil, nil, err
			}
			vulns := byUID[p.Identifier.UID]
			if vulns == nil {
				vulns = []vulnerability{}
			}
			matched += len(vulns)
			sort.Slice(vulns, func(i, j int) bool { return vulns[i].ID < vulns[j].ID })
			key := string(ctxJSON) + "\x00" + string(pkgJSON)
			i, seen := index[key]
			if !seen {
				i = len(pkgs)
				index[key] = i
				pkgs = append(pkgs, scanPackage{Context: ctxJSON, Package: pkgJSON, Vulnerabilities: vulns})
			} else if !reflect.DeepEqual(pkgs[i].Vulnerabilities, vulns) {
				// The same package matched differently in two places of the
				// image: the per-package model would not reproduce Trivy.
				return nil, nil, fmt.Errorf("%s %s: different vulnerabilities in different places of the image",
					p.Name, p.Version)
			}
			k := occKey{target: res.Target, pkg: i}
			if counts[k] == 0 {
				order = append(order, k)
			}
			counts[k]++
		}
		if matched != len(res.Vulnerabilities) {
			return nil, nil, fmt.Errorf("%s: %d vulnerabilities, %d attributed to a package", res.Target,
				len(res.Vulnerabilities), matched)
		}
	}
	occ := make([]occurrence, len(order))
	for i, k := range order {
		occ[i] = occurrence{Target: k.target, Package: k.pkg, Count: counts[k]}
	}
	if pkgs == nil {
		pkgs = []scanPackage{}
	}
	return pkgs, occ, nil
}

// contextSBOM builds the CycloneDX document of the packages of one context,
// with Trivy's own encoder, each package's BOM reference set to its registry
// key: Trivy carries it into the vulnerabilities it finds.
func contextSBOM(ctx context.Context, ctxJSON json.RawMessage, pkgs []archivedPackage) ([]byte, error) {
	var pctx pkgContext
	if err := json.Unmarshal(ctxJSON, &pctx); err != nil {
		return nil, fmt.Errorf("context: %w", err)
	}
	res := types.Result{Target: "stiva packages", Class: pctx.Class, Type: pctx.Type}
	for _, ap := range pkgs {
		var p ftypes.Package
		if err := json.Unmarshal(ap.Package, &p); err != nil {
			return nil, fmt.Errorf("package %s: %w", ap.Key, err)
		}
		p.Identifier.BOMRef = ap.Key
		res.Packages = append(res.Packages, p)
	}
	rep := types.Report{
		SchemaVersion: 2,
		ArtifactName:  "stiva-packages",
		ArtifactType:  ftypes.TypeContainerImage,
		Metadata:      types.Metadata{OS: pctx.OS},
		Results:       types.Results{res},
	}
	m := cyclonedx.NewMarshaler("stiva-scanner")
	bom, err := m.MarshalReport(ctx, rep)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := cdx.NewBOMEncoder(&buf, cdx.BOMFileFormatJSON).Encode(bom); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// contextVulnerabilities reads the vulnerabilities of an SBOM report by
// package key (the BOM reference). Every package gets an entry, possibly empty.
func contextVulnerabilities(rep *types.Report, pkgs []archivedPackage) (map[string][]vulnerability, error) {
	out := make(map[string][]vulnerability, len(pkgs))
	for _, ap := range pkgs {
		out[ap.Key] = []vulnerability{}
	}
	for _, res := range rep.Results {
		for _, v := range res.Vulnerabilities {
			key := v.PkgIdentifier.BOMRef
			if _, ok := out[key]; !ok {
				return nil, fmt.Errorf("%s: unknown package reference %q", v.VulnerabilityID, key)
			}
			sev, err := severity(v.Severity)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", v.VulnerabilityID, err)
			}
			out[key] = append(out[key], vulnerability{ID: v.VulnerabilityID, Severity: sev, Fixed: v.FixedVersion, Title: v.Title})
		}
	}
	for k := range out {
		vs := out[k]
		sort.Slice(vs, func(i, j int) bool { return vs[i].ID < vs[j].ID })
	}
	return out, nil
}
