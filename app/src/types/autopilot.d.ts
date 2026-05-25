interface ButtonConfig {
  id: string | number;
  label: string;
  value?: string | number;
}

interface ButtonConfigs {
  buttonsAlgo: ButtonConfig[];
  buttonsBuffer: ButtonConfig[];
  buttonMemoryAlgo: ButtonConfig[];
  buttonMemoryBuffer: ButtonConfig[];
}

interface InfoCardData {
  request?: string;
  limit?: string;
  [key: string]: any;
}

interface InfoCardProps {
  data: InfoCardData;
  type?: string;
}

interface VerticalAutopPilotFormProps {
  buttonConfigs?: ButtonConfigs;
  handleSelectedAlgo: (buttonId: any, buttonValue: any) => void;
  handleSelectedBuffer: (buttonId: any, buttonValue: any) => void;
  handleSelectedMemoryBuffer: (buttonId: any, buttonValue: any) => void;
  handleSelectedMemoryAlgo: (buttonId: any, buttonValue: any) => void;
  handleSelectedCpuLimit?: (buttonId: any, buttonValue: any) => void;
  handleSelectedMemLimit?: (buttonId: any, buttonValue: any) => void;
  data?: any;
  currentData: any;
  children?: React.ReactNode;
  activeButton: any;
  additionalInfoCPUAndMem: any;
  handleInputChange: (value: string, type: string, field: string, containerName: string) => void;
  isDisable?: boolean;
  reviewAutoOptimize?: boolean;
  containerName?: string;
  handleUpdateData: (data: any) => void;
  showKeepPreviousCpuLimit?: boolean;
  showKeepPreviousMemLimit?: boolean;
}
