const getMockData = async (key: string) => {
  const demoResp = await fetch('/api/public/mock/' + key);
  const demoData = await demoResp.json();
  return demoData;
};

export default getMockData;
