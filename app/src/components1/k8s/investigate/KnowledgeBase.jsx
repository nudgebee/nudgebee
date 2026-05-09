import { useEffect, useState } from 'react';
import apiKubernetes from '@api1/kubernetes';
import MarkDowns from '@components1/common/MarkDowns';

const KnowledgeBase = ({ troubleShootingEvent, preLoadedKnowledgeBase = [] }) => {
  const [knowledgeBase, setKnowledgeBase] = useState(preLoadedKnowledgeBase);
  const [, setLoading] = useState(false);

  useEffect(() => {
    if (preLoadedKnowledgeBase?.length > 0) {
      return;
    }
    const fetchKB = async () => {
      if (!troubleShootingEvent?.aggregation_key) {
        return;
      }

      try {
        setLoading(true);
        const res = await apiKubernetes.getKnowledgeBase(troubleShootingEvent.aggregation_key);
        if (res?.data) {
          setKnowledgeBase(res.data);
        }
      } catch (e) {
        console.error(e);
      } finally {
        setLoading(false);
      }
    };

    fetchKB();
  }, [troubleShootingEvent, preLoadedKnowledgeBase]);

  if (!knowledgeBase.length) {
    return null;
  }

  return (
    <>
      {knowledgeBase.map((base, index) => {
        let data = '';
        if (base.description) {
          data += base.description;
        }
        if (base.impact) {
          data += '\n\n' + base.impact;
        }
        if (base.diagnosis) {
          data += '\n\n' + base.diagnosis;
        }
        if (base.mitigation) {
          data += '\n\n' + base.mitigation;
        }

        return <MarkDowns key={index} data={data} sx={{ width: '100%' }} />;
      })}
    </>
  );
};

export default KnowledgeBase;
