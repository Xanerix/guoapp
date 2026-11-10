package core

type nativeSourceInfo struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	Group           string `json:"group"`
	GroupName       string `json:"groupName"`
	EntryName       string `json:"entryName"`
	DefaultOff      bool   `json:"defaultOff"`
	Available       bool   `json:"available"`
	Search          bool   `json:"search"`
	Suggestions     bool   `json:"suggestions"`
	Recommendations bool   `json:"recommendations"`
	Danmaku         bool   `json:"danmaku"`
	Devices         bool   `json:"devices"`
	Account         bool   `json:"account"`
	VIPPrompt       bool   `json:"vipPrompt"`
}

func (engine *nativeEngine) sourceList() []nativeSourceInfo {
	descriptors := map[string]SourceDescriptor{}
	order := append([]string{}, activeFlavor.sources...)
	listed := map[string]bool{}
	for _, id := range order {
		listed[id] = true
	}
	for _, descriptor := range registeredDescriptors() {
		descriptors[descriptor.ID] = descriptor
		if !listed[descriptor.ID] {
			order = append(order, descriptor.ID)
		}
	}
	list := make([]nativeSourceInfo, 0, len(order))
	for _, id := range order {
		descriptor, registered := descriptors[id]
		p, ok := engine.downloader.provider(id)
		if !registered || !ok {
			continue
		}
		info := nativeSourceInfo{
			ID: id, Name: descriptor.Name, Description: descriptor.Description,
			Group: descriptor.Group, GroupName: descriptor.GroupName, EntryName: descriptor.EntryName,
			DefaultOff: descriptor.DefaultOff, Available: nativeSourceAvailable(id), VIPPrompt: !descriptor.SkipVIPPrompt,
		}
		_, search := p.(Searcher)
		_, warningSearch := p.(WarningSearcher)
		info.Search = search || warningSearch
		_, info.Suggestions = p.(Suggester)
		_, info.Recommendations = p.(Recommender)
		_, info.Danmaku = p.(DanmakuProvider)
		_, info.Devices = p.(DeviceProvider)
		_, info.Account = p.(AccountProvider)
		list = append(list, info)
	}
	return list
}
