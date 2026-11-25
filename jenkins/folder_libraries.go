package jenkins

import (
	"encoding/xml"
	"fmt"
)

const (
	folderLibrariesProperty      = "org.jenkinsci.plugins.workflow.libs.FolderLibraries"
	folderLibrarySCMRetriever    = "org.jenkinsci.plugins.workflow.libs.SCMSourceRetriever"
	folderLibraryGitSCMSource    = "jenkins.plugins.git.GitSCMSource"
	folderLibraryBranchDiscovery = "<jenkins.plugins.git.traits.BranchDiscoveryTrait></jenkins.plugins.git.traits.BranchDiscoveryTrait>"
)

// folderLibraries is the FolderLibraries folder property. It is not a field of
// folderProperties: the property stays in folderProperties.Other as raw XML so
// that a folder whose libraries Terraform does not manage is written back
// byte for byte, including retrievers this provider cannot model.
type folderLibraries struct {
	Libraries []folderLibrary `xml:"libraries>org.jenkinsci.plugins.workflow.libs.LibraryConfiguration"`
}

type folderLibrary struct {
	Name                 string                  `xml:"name"`
	Retriever            *folderLibraryRetriever `xml:"retriever"`
	DefaultVersion       string                  `xml:"defaultVersion,omitempty"`
	Implicit             bool                    `xml:"implicit"`
	AllowVersionOverride bool                    `xml:"allowVersionOverride"`
	IncludeInChangesets  bool                    `xml:"includeInChangesets"`
}

type folderLibraryRetriever struct {
	Class string            `xml:"class,attr"`
	SCM   *folderLibrarySCM `xml:"scm"`
}

type folderLibrarySCM struct {
	Class         string              `xml:"class,attr"`
	ID            string              `xml:"id,omitempty"`
	Remote        string              `xml:"remote"`
	CredentialsID string              `xml:"credentialsId,omitempty"`
	Traits        folderLibraryTraits `xml:"traits"`
}

type folderLibraryTraits struct {
	Raw string `xml:",innerxml"`
}

// isGit reports whether the library is retrieved from a Git SCM source, the
// only retriever this provider models.
func (l folderLibrary) isGit() bool {
	return l.Retriever != nil &&
		l.Retriever.Class == folderLibrarySCMRetriever &&
		l.Retriever.SCM != nil &&
		l.Retriever.SCM.Class == folderLibraryGitSCMSource
}

// newGitFolderLibraryRetriever builds the retriever for a Git SCM source that
// discovers branches, which is what Jenkins itself writes for a library added
// through the UI with the default behaviours.
func newGitFolderLibraryRetriever(remote, credentialsID string) *folderLibraryRetriever {
	return &folderLibraryRetriever{
		Class: folderLibrarySCMRetriever,
		SCM: &folderLibrarySCM{
			Class:         folderLibraryGitSCMSource,
			Remote:        remote,
			CredentialsID: credentialsID,
			Traits:        folderLibraryTraits{Raw: folderLibraryBranchDiscovery},
		},
	}
}

// pipelineLibraries returns the libraries configured directly on the folder.
// A folder without the FolderLibraries property has none.
func (p *folderProperties) pipelineLibraries() ([]folderLibrary, error) {
	for _, property := range p.Other {
		if property.XMLName.Local != folderLibrariesProperty {
			continue
		}
		var libraries folderLibraries
		if err := xml.Unmarshal([]byte("<property>"+property.Raw+"</property>"), &libraries); err != nil {
			return nil, fmt.Errorf("could not parse %s: %w", folderLibrariesProperty, err)
		}
		return libraries.Libraries, nil
	}
	return nil, nil
}

// setPipelineLibraries replaces the FolderLibraries property with the given
// libraries, or removes the property when there are none. A Git SCM source
// keeps the id Jenkins assigned to the library of the same name, so an update
// that leaves a library alone does not hand it a new identity.
func (p *folderProperties) setPipelineLibraries(libraries []folderLibrary) error {
	existing, err := p.pipelineLibraries()
	if err != nil {
		return err
	}
	ids := map[string]string{}
	for _, library := range existing {
		if library.isGit() {
			ids[library.Name] = library.Retriever.SCM.ID
		}
	}
	for _, library := range libraries {
		if library.isGit() && library.Retriever.SCM.ID == "" {
			library.Retriever.SCM.ID = ids[library.Name]
		}
	}

	index, plugin := -1, ""
	for i, property := range p.Other {
		if property.XMLName.Local == folderLibrariesProperty {
			index, plugin = i, property.Plugin
			break
		}
	}

	if len(libraries) == 0 {
		if index >= 0 {
			p.Other = append(p.Other[:index], p.Other[index+1:]...)
		}
		return nil
	}

	raw, err := xml.Marshal(folderLibraries{Libraries: libraries})
	if err != nil {
		return fmt.Errorf("could not render %s: %w", folderLibrariesProperty, err)
	}
	// xml.Marshal wraps the value in an element named after the type; the
	// property only carries what is inside it.
	var inner struct {
		Raw string `xml:",innerxml"`
	}
	if err := xml.Unmarshal(raw, &inner); err != nil {
		return fmt.Errorf("could not render %s: %w", folderLibrariesProperty, err)
	}

	property := xmlRawProperty{
		XMLName: xml.Name{Local: folderLibrariesProperty},
		Plugin:  plugin,
		Raw:     inner.Raw,
	}
	if index >= 0 {
		p.Other[index] = property
	} else {
		p.Other = append(p.Other, property)
	}
	return nil
}
