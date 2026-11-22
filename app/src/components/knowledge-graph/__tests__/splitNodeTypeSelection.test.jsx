// Node Type selection -> backend filter arrays (#41503): a chosen type the picked account lacks
// must not turn into "no type filter at all".
import { splitNodeTypeSelection } from '../KnowledgeGraph';

// Unscoped list, and the same list once a GCP-only account is picked.
const allTypes = ['ComputeInstance', 'Disk', 'VPC'];
const allLeaves = { ComputeInstance: ['EC2Instance', 'GCEInstance'], Disk: ['EBSVolume', 'PersistentDisk'] };
const gcpTypes = ['ComputeInstance', 'Disk'];
const gcpLeaves = { ComputeInstance: ['GCEInstance'], Disk: ['PersistentDisk'] };

it('sends a full group as its node_type', () => {
  expect(splitNodeTypeSelection(['EBSVolume', 'PersistentDisk', 'VPC'], allTypes, allLeaves)).toEqual({
    nodeTypes: ['Disk', 'VPC'],
    specificTypes: [],
  });
});

it('keeps a type the account has', () => {
  expect(splitNodeTypeSelection(['EC2Instance'], allTypes, allLeaves)).toEqual({ nodeTypes: [], specificTypes: ['EC2Instance'] });
});

it('still filters when every selected type is missing from the account', () => {
  expect(splitNodeTypeSelection(['EC2Instance'], gcpTypes, gcpLeaves)).toEqual({ nodeTypes: [], specificTypes: ['EC2Instance'] });
  expect(splitNodeTypeSelection(['EC2Instance', 'VPC'], gcpTypes, gcpLeaves)).toEqual({
    nodeTypes: [],
    specificTypes: ['EC2Instance', 'VPC'],
  });
});

it('leaves out a missing type next to one the account has, so the two arrays are not ANDed to nothing', () => {
  expect(splitNodeTypeSelection(['EC2Instance', 'PersistentDisk'], gcpTypes, gcpLeaves)).toEqual({
    nodeTypes: ['Disk'],
    specificTypes: [],
  });
});

it('sends no filter when nothing is selected', () => {
  expect(splitNodeTypeSelection([], gcpTypes, gcpLeaves)).toEqual({ nodeTypes: [], specificTypes: [] });
  expect(splitNodeTypeSelection(undefined, gcpTypes, gcpLeaves)).toEqual({ nodeTypes: [], specificTypes: [] });
});
