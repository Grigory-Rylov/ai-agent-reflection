package modelsconfig

func NewTestHolder(cfg *ModelsConfig) *Holder {
	return &Holder{
		config:   cfg,
		filePath: "",
	}
}
