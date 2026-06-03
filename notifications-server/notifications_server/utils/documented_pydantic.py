import inspect
import json
import logging
from xml.etree import ElementTree

from docutils.core import publish_doctree
from pydantic import BaseModel
from pydantic.fields import FieldInfo


class DocstringField:
    """
    Represents a docstring field like ":var xyz: abc"
    For the above example, field_type is "var", field_target is "xyz", and field_value is "abc"
    """

    def __init__(self, name: str, body: str):
        field_type, field_target = name.split()
        self.field_type = field_type
        self.field_target = field_target
        self.field_value = body


class Docstring:
    def __init__(self, docstring: str):
        """
        Parse docutils/sphinx-style docs from a docstring.

        For example, given the following docstring:

            '''
            This is the description

            :var x: abc
            :example x: efg
            '''

        We parse:
        - description="This is the description"
        - fields:
            - var field
            - example field

        :param docstring: the docstring to parse
        """
        dom = publish_doctree(docstring).asdom()
        tree = ElementTree.fromstring(dom.toxml())
        self.fields = []

        for field in tree.iter(tag="field"):
            name = next(field.iter(tag="field_name"))
            body = next(field.iter(tag="field_body"))
            self.fields.append(DocstringField(name.text, "".join(body.itertext())))

        # we select all the docutils paragraphs which are under a root <block_quote> element
        # this is essentially all the lines in the docstring which don't contain :field:
        self.description = "\n\n".join(["".join(p.itertext()) for p in tree.findall("./block_quote/paragraph")])


class DocumentedModel(BaseModel):
    """
    Extends pydantic.BaseModel so that you can document models with docstrings and not using
        Field(..., description="foo")

    You write docs in the docstring and behind the scenes the actual Fields()  will be updated
    This way pydantic's introspection and schema-generation works like normal and includes those docs

    """

    # warning: __init_subclass__ only works on Python 3.6 and above
    def __init_subclass__(cls, **kwargs):
        super().__init_subclass__(**kwargs)
        docs = inspect.getdoc(cls)
        if docs is not None:
            cls.__update_fields_from_docstring(docs)

    @classmethod
    def __update_fields_from_docstring(cls, docstring):
        """
        Updates pydantic fields according to the docstring so that:

        1. you can document individual fields with ":var fieldname: description" in the model's docstring
        2. you can provide examples for individual fields with ":example fieldname: value" in the model's docstring
        3. docs about individual fields (like :var: and :example:) are removed from the root docs
        """
        docs = Docstring(docstring)
        for doc_field in docs.fields:
            if doc_field.field_target not in cls.model_fields:
                logging.warning(
                    f"The class {cls.__name__} has documentation for the `{doc_field.field_target}` field, but it"
                    " doesn't exist"
                )
                continue

            f: FieldInfo = cls.model_fields[doc_field.field_target]
            if doc_field.field_type == "example":
                existing = f.json_schema_extra or {}
                existing["example"] = cls.__parse_example(doc_field.field_value)
                f.json_schema_extra = existing
            if doc_field.field_type == "var":
                if f.description:
                    logging.warning(
                        f"Overriding existing field description '{f.description}' with '{doc_field.field_value}'"
                    )
                f.description = doc_field.field_value
        cls.__doc__ = docs.description

    @staticmethod
    def __parse_example(example: str):
        try:
            return json.loads(example)
        except json.JSONDecodeError:
            return example
