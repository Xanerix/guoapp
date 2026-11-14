package core

type nativeSourceInfo struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	Group           string `json:"group"`
	GroupName       string `json:"groupName"`
	EntryName       string `json:"entryName"`
	Available       bool   `json:"available"`
	Search          bool   `json:"search"`
	Suggestions     bool   `json:"suggestions"`
	Recommendations bool   `json:"recommendations"`
	Danmaku         bool   `json:"danmaku"`
	Devices         bool   `json:"devices"`
	Account         bool   `json:"account"`
	Metadata        bool   `json:"metadata"`
	VIPPrompt       bool   `json:"vipPrompt"`
}

func (engine *nativeEngine) sourceList() []nativeSourceInfo {
	registered := registeredDescriptors()
	list := make([]nativeSourceInfo, 0, len(registered))
	for _, descriptor := range registered {
		id := descriptor.ID
		p, ok := engine.downloader.provider(id)
		if !ok {
			continue
		}
		info := nativeSourceInfo{
			ID: id, Name: descriptor.Name, Description: descriptor.Description,
			Group: descriptor.Group, GroupName: descriptor.GroupName, EntryName: descriptor.EntryName,
			Available: nativeSourceAvailable(id), VIPPrompt: !descriptor.SkipVIPPrompt,
		}
		_, search := providerAs[Searcher](p)
		_, warningSearch := providerAs[WarningSearcher](p)
		info.Search = search || warningSearch
		_, info.Suggestions = providerAs[Suggester](p)
		_, info.Recommendations = providerAs[Recommender](p)
		_, info.Danmaku = providerAs[DanmakuProvider](p)
		_, info.Devices = providerAs[DeviceProvider](p)
		_, info.Account = providerAs[AccountProvider](p)
		_, info.Metadata = providerAs[MetadataEnricher](p)
		list = append(list, info)
	}
	return list
}
