<?xml version="1.0" encoding="UTF-8"?>
<!--
  GIB's UBL-TR schematron (UBL-TR_Main_Schematron.xml) was written for the ISO Schematron skeleton of 2010, which tolerates rules that sit outside any pattern, uses
  the xs prefix without declaring it and leaves the query binding open although its assertions use XPath 2 (castable as xs:date). Run after the includes are
  resolved, this identity transform declares the xs prefix, sets the binding to xslt2 and gathers the loose rules into a pattern of their own, which SchXslt needs.
  It changes no assertion.
-->
<xsl:transform version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:sch="http://purl.oclc.org/dsdl/schematron">
  <xsl:template match="@* | node()">
    <xsl:copy><xsl:apply-templates select="@* | node()"/></xsl:copy>
  </xsl:template>

  <xsl:template match="sch:schema">
    <xsl:copy>
      <xsl:apply-templates select="@*"/>
      <xsl:attribute name="queryBinding">xslt2</xsl:attribute>
      <sch:ns prefix="xs" uri="http://www.w3.org/2001/XMLSchema"/>
      <xsl:apply-templates select="node()[not(self::sch:rule)]"/>
      <xsl:if test="sch:rule">
        <sch:pattern id="general-rules"><xsl:apply-templates select="sch:rule"/></sch:pattern>
      </xsl:if>
    </xsl:copy>
  </xsl:template>
</xsl:transform>
